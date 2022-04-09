package impl

import (
	"context"
	"fmt"
	"math/big"
	"reflect"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	logger "github.com/rs/zerolog/log"
	"github.com/textileio/go-tableland/pkg/eventprocessor/eventfeed"
	tbleth "github.com/textileio/go-tableland/pkg/tableregistry/impl/ethereum"
)

var log = logger.With().Str("component", "eventfeed").Logger()

type EventFeed struct {
	ethClient   eventfeed.EthClient
	scAddress   common.Address
	contractAbi *abi.ABI

	config *eventfeed.Config
}

func New(ethClient eventfeed.EthClient, scAddress common.Address, opts ...eventfeed.Option) (*EventFeed, error) {
	config := eventfeed.DefaultConfig()
	for _, o := range opts {
		if err := o(config); err != nil {
			return nil, fmt.Errorf("applying provided option: %s", err)
		}
	}

	contractAbi, err := tbleth.ContractMetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("get contract-abi: %s", err)
	}
	return &EventFeed{
		ethClient:   ethClient,
		scAddress:   scAddress,
		contractAbi: contractAbi,
		config:      config,
	}, nil
}

func (ef *EventFeed) Start(ctx context.Context, fromHeight int64, ch chan<- eventfeed.BlockEvents, filterEventTypes []eventfeed.EventType) error {
	// Spinup a background process that will post to chHeads when a new block is detected.
	// This channel will be the heart-beat to pull new logs from the chain.
	//
	// We defer the ctx cancelation to be sure we always gracefully close this background go routine
	// in any event that returns this function.
	ctx, cls := context.WithCancel(ctx)
	defer cls()
	chHeads := make(chan *types.Header, 1)
	if err := ef.notifyNewHeads(ctx, chHeads); err != nil {
		return fmt.Errorf("creating background head notificator: %s", err)
	}

	// Create filterTopics that will be used to only listening for the desired events.
	filterTopics, err := ef.getTopicsForEventTypes(filterEventTypes)
	if err != nil {
		return fmt.Errorf("creating topics for filtered event types: %s", err)
	}

	// Listen for new blocks, and get new events.
	for {
		select {
		case h, ok := <-chHeads:
			if !ok {
				log.Info().Msg("new head channel was closed, closing gracefully")
				return nil
			}
			// TODO(jsign): increase a counter metric
			log.Debug().Int64("height", h.Number.Int64()).Msg("received new chain header")

			// Recall that we only accept as "final" blocks the one that are at least
			// minChainDepth behind the new known head. This is done to avoid reorgs
			// sideffects.
			toHeight := h.Number.Int64() - int64(ef.config.MinBlockChainDepth)
			if toHeight < fromHeight {
				continue
			}

			// Put a cap on how big the query will be. This is important if we are
			// doing a cold syncing or have fall behind after a long stop.
			// i.e: asking for events in a 100k blocks can cause problems in the API
			// call, require too much bandwidth or memory.
			// Note that toHeight and fromHeight are inclusive.
			if toHeight-fromHeight+1 > int64(ef.config.MaxEventsBatchSize) {
				toHeight = fromHeight + int64(ef.config.MaxEventsBatchSize) - 1
			}

			// Ask for the desired events between fromHeight to toHeight.
			query := ethereum.FilterQuery{
				FromBlock: big.NewInt(fromHeight),
				ToBlock:   big.NewInt(toHeight),
				Addresses: []common.Address{ef.scAddress},
				Topics:    [][]common.Hash{filterTopics},
			}
			logs, err := ef.ethClient.FilterLogs(ctx, query)
			if err != nil {
				// If we got an error here, log it but allow to be retried
				// in the next head. Probably the API can have transient unavailability.
				log.Warn().Err(err).Msgf("filter logs from %d to %d", fromHeight, toHeight)
				continue
			}

			// If there're no events, nothing to do here.
			if len(logs) == 0 {
				continue
			}

			// We received new events. We'll group/pack them by block number in
			// BLockEvents structs, and send them to the `ch` channel provided
			// by the caller.
			bq := eventfeed.BlockEvents{
				BlockNumber: int64(logs[0].BlockNumber),
			}
			for _, l := range logs {
				if bq.BlockNumber != int64(l.BlockNumber) {
					ch <- bq
					bq = eventfeed.BlockEvents{
						BlockNumber: int64(l.BlockNumber),
					}
				}

				event, err := ef.parseEvent(l)
				if err != nil {
					return fmt.Errorf("couldn't parse event: %s", err)
				}
				bq.Events = append(bq.Events, event)
			}
			// Sent last block events construction of the loop.
			ch <- bq

			// Update our fromHeight to the latest processed height plus one.
			fromHeight = bq.BlockNumber + 1
			// TODO(jsign): metric for "fromHeight"
		}
	}
}

// parseEvent deconstructs a raw event that was received from the Ethereum node,
// to a structured representation. Since the event can be from different types,
// we return an interface.
// Every possible type in the interface{} is an auto-generated struct by
// `make ethereum` named `Contract*` (e.g: ContractRunSQL, ContractTransfer, etc).
// See this mapping in the `supportedEvents` map global variable in this file.
func (ef *EventFeed) parseEvent(l types.Log) (interface{}, error) {
	// We get an event descriptior from the common.Hash value that is always
	// in Topic[0] in events. This is an ID for the kind of event.
	eventDescr, err := ef.contractAbi.EventByID(l.Topics[0])
	if err != nil {
		return nil, fmt.Errorf("detecting event type: %s", err)
	}

	se, ok := eventfeed.SupportedEvents[eventfeed.EventType(eventDescr.Name)]
	if !ok {
		return nil, fmt.Errorf("unknown event type %s", eventDescr.Name)
	}
	// Create a new *ContractXXXX struct that corresponds to this event.
	// e.g: *ContractRunSQL if this event was one fired by runSQL(..) SC function.
	i := reflect.New(se).Interface()

	// Now we unmarshal the event data, to the *ContractXXX struct.
	// First, we unmarshal the information contained in the `data` of the event, which
	// are non-indexed fields of the event.
	if len(l.Data) > 0 {
		if err := ef.contractAbi.UnpackIntoInterface(i, eventDescr.Name, l.Data); err != nil {
			return nil, fmt.Errorf("unpacking into interface: %s", err)
		}
	}
	// Second, we unmarshal indexed fields which aren't in data but in Topics[:1].
	var indexed abi.Arguments
	for _, arg := range eventDescr.Inputs {
		if arg.Indexed {
			indexed = append(indexed, arg)
		}
	}
	if err := abi.ParseTopics(i, indexed, l.Topics[1:]); err != nil {
		return nil, fmt.Errorf("unpacking indexed topics: %s", err)
	}
	// Note that the above two steps of unmarshalling isn't something particular
	// to us, it's just how Ethereum works.
	// This parsedEvent(...) function was coded in a generic way, so it will hardly
	// ever change.

	// TODO(jsign): add counter of parsed events per event type.

	return i, nil
}

func (ef *EventFeed) getTopicsForEventTypes(ets []eventfeed.EventType) ([]common.Hash, error) {
	for _, fet := range ets {
		if _, ok := eventfeed.SupportedEvents[fet]; !ok {
			return nil, fmt.Errorf("event type filter %s isn't supported", fet)
		}
	}
	topics := make([]common.Hash, len(ets))
	for i, et := range ets {
		e, ok := ef.contractAbi.Events[string(et)]
		if !ok {
			return nil, fmt.Errorf("event type %s wasn't found in compiled contract", et)
		}
		topics[i] = e.ID
	}
	return topics, nil
}

// notifyNewHeads will send to the provided channel new detected heads in the chain.
// It's mandatory that the caller cancels the provided context to gracefully close the background process.
// When this happens the provided channel will be closed.
func (ef *EventFeed) notifyNewHeads(ctx context.Context, ch chan *types.Header) error {
	subHeader, err := ef.ethClient.SubscribeNewHead(ctx, ch)
	if err != nil {
		return fmt.Errorf("subscribing to new heads: %s", err)
	}
	go func() {
		defer subHeader.Unsubscribe()
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("gracefully closing new heads subscription")
				return
			case err := <-subHeader.Err():
				log.Error().Err(err).Msg("new heads subscription")
				return
			}
		}
	}()
	return nil
}

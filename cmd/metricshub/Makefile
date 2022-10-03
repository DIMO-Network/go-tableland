include .env
export

check-env:
ifndef API_KEY
	$(error API_KEY is undefined)
endif

deploy-testnet: check-env
	gcloud run deploy metricshub-testnet --source=${PWD}  --set-env-vars="GCP_PROJECT=textile-310716,BIGQUERY_DATASET=tableland_testnet,BIGQUERY_TABLE=system_metrics,API_KEY=${API_KEY}" --region="us-west1" --allow-unauthenticated

deploy-staging: check-env
	gcloud run deploy metricshub-staging --source=${PWD}  --set-env-vars="GCP_PROJECT=textile-310716,BIGQUERY_DATASET=tableland_staging,BIGQUERY_TABLE=system_metrics,API_KEY=${API_KEY}" --region="us-west1" --allow-unauthenticated
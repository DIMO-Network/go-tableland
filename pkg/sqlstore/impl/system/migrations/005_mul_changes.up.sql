ALTER TABLE system_tables DROP COLUMN type;
ALTER TABLE system_tables DROP COLUMN uuid;
ALTER TABLE system_tables DROP COLUMN controller;
ALTER TABLE system_tables ADD COLUMN id NUMERIC(60) PRIMARY KEY CHECK (id >= 0);
ALTER TABLE system_tables ADD COLUMN structure VARCHAR(64) NOT NULL;
ALTER TABLE system_tables ADD COLUMN controller VARCHAR(64) NOT NULL;
ALTER TABLE system_tables ADD COLUMN description VARCHAR(100) NOT NULL;
ALTER TABLE system_tables ADD COLUMN name VARCHAR(50) NOT NULL;


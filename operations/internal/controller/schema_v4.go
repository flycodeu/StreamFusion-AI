package controller

const schemaV4 = `ALTER TABLE artifacts ADD COLUMN recipe TEXT NOT NULL DEFAULT 'java-compose-v1' CHECK(recipe IN ('java-compose-v1','preview-node-v1'));`

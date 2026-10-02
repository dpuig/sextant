ALTER TABLE objects ADD COLUMN labels jsonb NOT NULL DEFAULT '{}';
CREATE INDEX objects_labels_idx ON objects USING gin (labels);

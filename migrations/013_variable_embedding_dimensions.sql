-- +goose Up
CREATE EXTENSION IF NOT EXISTS vector;

DROP INDEX IF EXISTS chunks_embedding_idx;
DROP INDEX IF EXISTS claims_embedding_idx;
DROP INDEX IF EXISTS entities_embedding_idx;

ALTER TABLE chunks
  ALTER COLUMN embedding TYPE vector USING embedding::vector;

ALTER TABLE claims
  ALTER COLUMN embedding TYPE vector USING embedding::vector;

ALTER TABLE entities
  ALTER COLUMN embedding TYPE vector USING embedding::vector;

CREATE INDEX IF NOT EXISTS chunks_embedding_768_idx
  ON chunks USING hnsw ((embedding::vector(768)) vector_cosine_ops)
  WHERE embedding_dimensions = 768;

CREATE INDEX IF NOT EXISTS chunks_embedding_1024_idx
  ON chunks USING hnsw ((embedding::vector(1024)) vector_cosine_ops)
  WHERE embedding_dimensions = 1024;

CREATE INDEX IF NOT EXISTS chunks_embedding_1280_idx
  ON chunks USING hnsw ((embedding::vector(1280)) vector_cosine_ops)
  WHERE embedding_dimensions = 1280;

CREATE INDEX IF NOT EXISTS chunks_embedding_1536_idx
  ON chunks USING hnsw ((embedding::vector(1536)) vector_cosine_ops)
  WHERE embedding_dimensions = 1536;

CREATE INDEX IF NOT EXISTS claims_embedding_768_idx
  ON claims USING hnsw ((embedding::vector(768)) vector_cosine_ops)
  WHERE embedding_dimensions = 768;

CREATE INDEX IF NOT EXISTS claims_embedding_1024_idx
  ON claims USING hnsw ((embedding::vector(1024)) vector_cosine_ops)
  WHERE embedding_dimensions = 1024;

CREATE INDEX IF NOT EXISTS claims_embedding_1280_idx
  ON claims USING hnsw ((embedding::vector(1280)) vector_cosine_ops)
  WHERE embedding_dimensions = 1280;

CREATE INDEX IF NOT EXISTS claims_embedding_1536_idx
  ON claims USING hnsw ((embedding::vector(1536)) vector_cosine_ops)
  WHERE embedding_dimensions = 1536;

CREATE INDEX IF NOT EXISTS entities_embedding_768_idx
  ON entities USING hnsw ((embedding::vector(768)) vector_cosine_ops)
  WHERE embedding IS NOT NULL AND embedding_dimensions = 768;

CREATE INDEX IF NOT EXISTS entities_embedding_1024_idx
  ON entities USING hnsw ((embedding::vector(1024)) vector_cosine_ops)
  WHERE embedding IS NOT NULL AND embedding_dimensions = 1024;

CREATE INDEX IF NOT EXISTS entities_embedding_1280_idx
  ON entities USING hnsw ((embedding::vector(1280)) vector_cosine_ops)
  WHERE embedding IS NOT NULL AND embedding_dimensions = 1280;

CREATE INDEX IF NOT EXISTS entities_embedding_1536_idx
  ON entities USING hnsw ((embedding::vector(1536)) vector_cosine_ops)
  WHERE embedding IS NOT NULL AND embedding_dimensions = 1536;

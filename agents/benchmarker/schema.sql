CREATE TABLE IF NOT EXISTS benchmarks (
    id UUID PRIMARY KEY,
    model VARCHAR(255) NOT NULL,
    backend VARCHAR(255) NOT NULL,
    device_class VARCHAR(255) NOT NULL,
    tokens_per_second DOUBLE PRECISION NOT NULL,
    completion_tokens INTEGER NOT NULL,
    duration_seconds DOUBLE PRECISION NOT NULL,
    samples INTEGER NOT NULL,
    batch_size INTEGER NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL
);
ALTER TABLE benchmarks ADD COLUMN IF NOT EXISTS task VARCHAR(32) NOT NULL DEFAULT 'generation';
ALTER TABLE benchmarks ADD COLUMN IF NOT EXISTS prompt_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE benchmarks ADD COLUMN IF NOT EXISTS documents INTEGER NOT NULL DEFAULT 0;
ALTER TABLE benchmarks ADD COLUMN IF NOT EXISTS documents_per_second DOUBLE PRECISION NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_benchmarks_model ON benchmarks(model);
CREATE INDEX IF NOT EXISTS idx_benchmarks_backend ON benchmarks(backend);
CREATE INDEX IF NOT EXISTS idx_benchmarks_timestamp ON benchmarks(timestamp DESC);

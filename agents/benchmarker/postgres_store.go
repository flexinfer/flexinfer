package benchmarker

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// PostgresStore saves benchmark results to a PostgreSQL database.
type PostgresStore struct {
	db         *sql.DB
	kubeClient kubernetes.Interface
}

func NewPostgresStore(dsn string, client kubernetes.Interface) (*PostgresStore, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres connection: %w", err)
	}

	// Ping to ensure connection is valid
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	return &PostgresStore{
		db:         db,
		kubeClient: client, // Required to lookup node labels for device class
	}, nil
}

func (p *PostgresStore) Save(ctx context.Context, r BenchmarkRecord) error {
	logger := log.FromContext(ctx)

	// We need the node's device class.
	deviceClass := "unknown"
	if r.NodeName != "" && p.kubeClient != nil {
		node, err := p.kubeClient.CoreV1().Nodes().Get(ctx, r.NodeName, metav1.GetOptions{})
		if err != nil {
			logger.Error(err, "failed to get node labels for postgres store, using 'unknown'")
		} else {
			deviceClass = deviceClassFromNode(node)
		}
	}

	id := uuid.New().String()

	query := `
		INSERT INTO benchmarks (
			id, model, backend, device_class, tokens_per_second, 
			completion_tokens, duration_seconds, samples, batch_size, timestamp
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		)
	`

	_, err := p.db.ExecContext(ctx, query,
		id,
		r.ModelName,
		r.Backend,
		deviceClass,
		r.TokensPerSecond,
		r.CompletionTokens,
		r.Duration.Seconds(),
		r.Samples,
		r.BatchSize,
		r.Timestamp,
	)

	if err != nil {
		return fmt.Errorf("failed to insert benchmark result into postgres: %w", err)
	}

	logger.Info("Saved benchmark result to postgres", "id", id, "model", r.ModelName)
	return nil
}

// Close closes the underlying database connection
func (p *PostgresStore) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}

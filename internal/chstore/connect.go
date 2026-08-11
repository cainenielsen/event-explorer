package chstore

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"eventquery/internal/config"
)

// Open connects to ClickHouse over the native protocol and runs the schema
// migration before handing back a ready-to-use connection.
func Open(ctx context.Context, cfg config.ClickHouse) (driver.Conn, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{cfg.Addr},
		// Deliberately NOT cfg.Database: that database may not exist yet on
		// first boot, and asking the server to switch into a nonexistent
		// default database fails the handshake. We connect against
		// ClickHouse's always-present "default" database, create ours via
		// Migrate below, and fully-qualify every query with it afterwards
		// (see Store).
		Auth: clickhouse.Auth{
			Database: "default",
			Username: cfg.Username,
			Password: cfg.Password,
		},
		DialTimeout:     10 * time.Second,
		ConnMaxLifetime: time.Hour,
		// Generous pool sizing: the seeder opens one Store and fans batch
		// inserts out across NumCPU goroutines, each holding a pooled
		// connection for the life of a batch.
		MaxOpenConns: 32,
		MaxIdleConns: 32,
		Settings: clickhouse.Settings{
			// Lets the automatic query planner move eligible WHERE
			// conditions into PREWHERE for us (see design notes: PREWHERE
			// forces the filter column to be read/evaluated before the rest
			// of the row). This is ClickHouse's default, set explicitly so
			// the behavior doesn't silently depend on server defaults.
			"optimize_move_to_prewhere": 1,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("open clickhouse connection: %w", err)
	}

	if err := conn.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping clickhouse: %w", err)
	}

	if err := Migrate(ctx, conn, cfg.Database); err != nil {
		return nil, err
	}

	return conn, nil
}

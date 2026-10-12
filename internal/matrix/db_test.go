package matrix

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestOpenDB_PrivateFiles pins that the database and its WAL companions
// are readable only by the owner: they hold decrypted messages and the
// access token. It covers a fresh database and one left at 0644 by an
// earlier version.
func TestOpenDB_PrivateFiles(t *testing.T) {
	for _, existing := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, "mtx.db")
		if existing {
			for _, name := range []string{path, path + "-wal", path + "-shm"} {
				if err := os.WriteFile(name, nil, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(name, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		db, err := openDB(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(context.Background(), `CREATE TABLE t (n INTEGER)`); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{path, path + "-wal", path + "-shm"} {
			fi, err := os.Stat(name)
			if err != nil {
				t.Fatal(err)
			}
			if mode := fi.Mode().Perm(); mode != 0o600 {
				t.Errorf("existing=%v: %s mode = %o, want 600", existing, filepath.Base(name), mode)
			}
		}
		db.Close()
	}
}

// TestOpenDB_ConcurrentWritersWait pins the fix for events stuck
// undecryptable after a key backup import: hicli retries decryption in
// one goroutine per received session, each a read-then-write
// transaction. With SQLite's default deferred transactions in WAL mode,
// a writer that started as a reader fails at once with "database is
// locked" (busy_timeout does not apply to that upgrade), and hicli never
// retries the save. openDB must make such writers wait instead.
func TestOpenDB_ConcurrentWritersWait(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(filepath.Join(t.TempDir(), "has space?#.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ctx, `CREATE TABLE t (n INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO t VALUES (0)`); err != nil {
		t.Fatal(err)
	}

	const writers = 20
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- db.DoTxn(ctx, nil, func(ctx context.Context) error {
				var n int
				if err := db.QueryRow(ctx, `SELECT n FROM t`).Scan(&n); err != nil {
					return err
				}
				_, err := db.Exec(ctx, `UPDATE t SET n = $1`, n+1)
				return err
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent write transaction failed: %v", err)
		}
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT n FROM t`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != writers {
		t.Errorf("n = %d, want %d (lost updates)", n, writers)
	}
}

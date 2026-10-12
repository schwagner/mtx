package matrix

import (
	"errors"
	"io/fs"
	"net/url"
	"os"

	"go.mau.fi/util/dbutil"
)

// openDB opens the SQLite database at path the way gomuks does
// (pkg/gomuks/gomuks.go): transactions begin IMMEDIATE, so concurrent
// writers queue on busy_timeout. With the default deferred transactions a
// read-then-write transaction fails at once with "database is locked"
// when another writer got there first, and hicli does not retry the
// saves it loses that way (decrypted events after a key backup import).
func openDB(path string) (*dbutil.Database, error) {
	if err := makePrivate(path); err != nil {
		return nil, err
	}
	uri := "file:" + (&url.URL{Path: path}).EscapedPath() + "?_txlock=immediate"
	db, err := dbutil.NewWithDialect(uri, "sqlite3-fk-wal")
	if err != nil {
		return nil, err
	}
	db.RawDB.SetMaxOpenConns(5)
	db.RawDB.SetMaxIdleConns(1)
	return db, nil
}

// makePrivate makes the database at path and its WAL companions readable
// only by the owner: they hold decrypted messages and the access token.
// It creates the main file 0600 if missing, since SQLite gives the -wal
// and -shm files it creates later the main file's mode, and tightens
// files an earlier version left at the umask default.
func makePrivate(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(name, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

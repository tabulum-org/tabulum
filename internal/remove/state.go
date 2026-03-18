package remove

import (
	"encoding/json"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

var stateBucket = []byte("state")

type stateEntry struct {
	Key            string    `json:"key"`
	Value          string    `json:"value"`
	LastModifiedBy string    `json:"last_modified_by"`
	LastModifiedAt time.Time `json:"last_modified_at"`
}

// RemoveStateKey deletes a key from the state DB and writes a transparency log entry.
// The kernel must be stopped (bbolt exclusive lock). Returns the removed value for hashing.
func RemoveStateKey(stateDBPath, transparencyPath, key, reason string) error {
	db, err := bolt.Open(stateDBPath, 0o644, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return fmt.Errorf("open state DB (is the kernel stopped?): %w", err)
	}
	defer db.Close()

	var value string
	var found bool

	err = db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(stateBucket)
		if b == nil {
			return fmt.Errorf("state bucket does not exist")
		}

		data := b.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("key %q not found in state DB", key)
		}

		var entry stateEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			// Raw value — hash it directly
			value = string(data)
		} else {
			value = entry.Value
		}
		found = true

		return b.Delete([]byte(key))
	})
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("key %q not found", key)
	}

	entry := TransparencyEntry{
		Timestamp:   time.Now().UTC(),
		Action:      "state_key_removed",
		Key:         key,
		Reason:      reason,
		PerformedBy: "infrastructure_operator",
		ContentHash: HashContent(value),
	}

	return AppendTransparencyEntry(transparencyPath, entry)
}

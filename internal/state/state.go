package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/tabulum/tabulum/internal/logging"
	bolt "go.etcd.io/bbolt"
)

var (
	bucketName     = []byte("state")
	ErrKeyNotFound = errors.New("key not found")
	ErrKeyTooLong  = errors.New("key exceeds maximum length")
	ErrValueTooBig = errors.New("value exceeds maximum size")
	ErrAtCapacity  = errors.New("state store at capacity")
)

type Entry struct {
	Key            string    `json:"key"`
	Value          string    `json:"value"`
	LastModifiedBy string    `json:"last_modified_by"`
	LastModifiedAt time.Time `json:"last_modified_at"`
}

type Capacity struct {
	UsedBytes  int64 `json:"used_bytes"`
	TotalBytes int64 `json:"total_bytes"`
	KeyCount   int   `json:"key_count"`
}

type Store struct {
	db          *bolt.DB
	eventLog    *logging.EventLog
	maxCapacity int64
	maxKeyLen   int
	maxValueLen int
	usedBytes   atomic.Int64
	keyCount    atomic.Int64
}

func NewStore(dataDir string, maxCapacity int64, maxKeyLen int, maxValueLen int) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "state.db")
	db, err := bolt.Open(dbPath, 0o644, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open state db: %w", err)
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketName)
		return err
	}); err != nil {
		db.Close()
		return nil, err
	}

	s := &Store{
		db:          db,
		maxCapacity: maxCapacity,
		maxKeyLen:   maxKeyLen,
		maxValueLen: maxValueLen,
	}

	// Initialize running counters from existing data
	if err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		c := b.Cursor()
		var used int64
		var count int64
		for k, v := c.First(); k != nil; k, v = c.Next() {
			count++
			used += int64(len(k) + len(v))
		}
		s.usedBytes.Store(used)
		s.keyCount.Store(count)
		return nil
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("init capacity counters: %w", err)
	}

	return s, nil
}

func (s *Store) SetEventLog(log interface{}) {
	if el, ok := log.(*logging.EventLog); ok {
		s.eventLog = el
	}
}

func (s *Store) Get(ctx context.Context, key string, readBy string) (*Entry, error) {
	if len(key) > s.maxKeyLen {
		return nil, ErrKeyTooLong
	}

	var entry *Entry
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		data := b.Get([]byte(key))
		if data == nil {
			return ErrKeyNotFound
		}
		entry = &Entry{}
		return json.Unmarshal(data, entry)
	})
	if err != nil {
		return nil, err
	}

	// Log the read event
	if s.eventLog != nil {
		_ = s.eventLog.Append(ctx, logging.Event{
			Type:         logging.EventStateRead,
			AgentAddress: readBy,
			Data: logging.StateReadData{
				Key:    key,
				ReadBy: readBy,
				Found:  true,
			},
		})
	}

	return entry, nil
}

func (s *Store) Set(ctx context.Context, key string, value string, writtenBy string) error {
	if len(key) > s.maxKeyLen {
		return ErrKeyTooLong
	}
	if len(value) > s.maxValueLen {
		return ErrValueTooBig
	}

	entry := Entry{
		Key:            key,
		Value:          value,
		LastModifiedBy: writtenBy,
		LastModifiedAt: time.Now().UTC(),
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	newEntrySize := int64(len(key) + len(data))

	// Log event BEFORE writing to db
	if s.eventLog != nil {
		if err := s.eventLog.Append(ctx, logging.Event{
			Type:         logging.EventStateWritten,
			AgentAddress: writtenBy,
			Data: logging.StateWrittenData{
				Key:       key,
				Value:     value,
				Length:    len(value),
				WrittenBy: writtenBy,
			},
		}); err != nil {
			return fmt.Errorf("log state write: %w", err)
		}
	}

	// Atomic capacity check and write within a single bbolt transaction
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		existing := b.Get([]byte(key))
		var oldSize int64
		if existing != nil {
			oldSize = int64(len(key) + len(existing))
		}

		// Check capacity: current usage minus old entry plus new entry
		currentUsed := s.usedBytes.Load()
		if existing == nil && currentUsed+newEntrySize > s.maxCapacity {
			return ErrAtCapacity
		}
		if existing != nil && currentUsed-oldSize+newEntrySize > s.maxCapacity {
			return ErrAtCapacity
		}

		if err := b.Put([]byte(key), data); err != nil {
			return err
		}

		// Update running counters
		if existing == nil {
			s.keyCount.Add(1)
			s.usedBytes.Add(newEntrySize)
		} else {
			s.usedBytes.Add(newEntrySize - oldSize)
		}
		return nil
	})
}

func (s *Store) Delete(ctx context.Context, key string, deletedBy string) error {
	if len(key) > s.maxKeyLen {
		return ErrKeyTooLong
	}

	// Log event BEFORE deleting
	if s.eventLog != nil {
		// Check existence before logging to avoid logging deletes of non-existent keys
		var exists bool
		s.db.View(func(tx *bolt.Tx) error {
			exists = tx.Bucket(bucketName).Get([]byte(key)) != nil
			return nil
		})
		if !exists {
			return ErrKeyNotFound
		}
		if err := s.eventLog.Append(ctx, logging.Event{
			Type:         logging.EventStateDeleted,
			AgentAddress: deletedBy,
			Data: logging.StateDeletedData{
				Key:       key,
				DeletedBy: deletedBy,
			},
		}); err != nil {
			return fmt.Errorf("log state delete: %w", err)
		}
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		existing := b.Get([]byte(key))
		if existing == nil {
			return ErrKeyNotFound
		}
		oldSize := int64(len(key) + len(existing))
		if err := b.Delete([]byte(key)); err != nil {
			return err
		}
		s.usedBytes.Add(-oldSize)
		s.keyCount.Add(-1)
		return nil
	})
}

func (s *Store) ListKeys(_ context.Context, prefix string, cursor string, limit int) (keys []string, nextCursor string, total int, err error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	err = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		c := b.Cursor()

		// Count total matching keys
		if prefix == "" {
			total = b.Stats().KeyN
		} else {
			prefixBytes := []byte(prefix)
			for k, _ := c.Seek(prefixBytes); k != nil && hasPrefix(k, prefixBytes); k, _ = c.Next() {
				total++
			}
		}

		// Navigate to start position
		var k, v []byte
		if cursor != "" {
			k, v = c.Seek([]byte(cursor))
			// Skip the cursor key itself (it was in the previous page)
			if k != nil && string(k) == cursor {
				k, v = c.Next()
			}
		} else if prefix != "" {
			k, v = c.Seek([]byte(prefix))
		} else {
			k, v = c.First()
		}
		_ = v

		prefixBytes := []byte(prefix)
		collected := 0
		for ; k != nil && collected < limit; k, _ = c.Next() {
			if prefix != "" && !hasPrefix(k, prefixBytes) {
				break
			}
			keys = append(keys, string(k))
			collected++
		}

		// Check if there's a next page
		if k != nil && (prefix == "" || hasPrefix(k, prefixBytes)) {
			if len(keys) > 0 {
				nextCursor = keys[len(keys)-1]
			}
		}

		return nil
	})
	return
}

func hasPrefix(s, prefix []byte) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := range prefix {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}

func (s *Store) GetCapacity(_ context.Context) (*Capacity, error) {
	return &Capacity{
		UsedBytes:  s.usedBytes.Load(),
		TotalBytes: s.maxCapacity,
		KeyCount:   int(s.keyCount.Load()),
	}, nil
}

func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// --- Recovery ---

func (s *Store) ApplyStateWrite(key string, value string, writtenBy string, at time.Time) error {
	entry := Entry{
		Key:            key,
		Value:          value,
		LastModifiedBy: writtenBy,
		LastModifiedAt: at,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		existing := b.Get([]byte(key))
		if err := b.Put([]byte(key), data); err != nil {
			return err
		}
		newSize := int64(len(key) + len(data))
		if existing == nil {
			s.keyCount.Add(1)
			s.usedBytes.Add(newSize)
		} else {
			oldSize := int64(len(key) + len(existing))
			s.usedBytes.Add(newSize - oldSize)
		}
		return nil
	})
}

func (s *Store) ApplyStateDelete(key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		existing := b.Get([]byte(key))
		if existing == nil {
			return nil
		}
		oldSize := int64(len(key) + len(existing))
		if err := b.Delete([]byte(key)); err != nil {
			return err
		}
		s.usedBytes.Add(-oldSize)
		s.keyCount.Add(-1)
		return nil
	})
}

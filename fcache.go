package fcache

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Cache struct {
	items map[string]*Item
	mu    sync.Mutex

	MemoryExpiration time.Duration
	DiskExpiration   time.Duration

	Dir string
}

func New(dir string, diskExpiration, memoryExpiration time.Duration) (*Cache, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &Cache{
		items:            make(map[string]*Item),
		Dir:              dir,
		DiskExpiration:   diskExpiration,
		MemoryExpiration: memoryExpiration,
	}, nil
}

func (c *Cache) Get(k string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if item, ok := c.items[k]; ok {
		data, err := item.Read()
		if err != nil {
			return nil, err
		} else {
			return data, nil
		}
	} else {
		return nil, fmt.Errorf("no key found in cache")
	}
}

func (c *Cache) Set(k string, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if item, ok := c.items[k]; ok {
		item.Data = data
		if err := c.replaceFile(item.Handle, data); err != nil {
			return fmt.Errorf("file replace failed: %w", err)
		}
	} else {
		file, err := c.createFile(k+".fc", data)
		if err != nil {
			return fmt.Errorf("file write failed: %w", err)
		}

		now := time.Now()
		c.items[k] = &Item{
			DiskExpiration:   now.Add(c.DiskExpiration).Unix(),
			MemoryExpiration: now.Add(c.MemoryExpiration).Unix(),
			Data:             data,
			Handle:           file,
		}
	}

	return nil
}

func (c *Cache) replaceFile(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("failed to write data to file: %w", err)
	}

	return nil
}

func (c *Cache) createFile(name string, data []byte) (*os.File, error) {
	f, err := os.Create(filepath.Join(c.Dir, name))
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		return nil, fmt.Errorf("failed to write data to file: %w", err)
	}

	return f, nil
}

type Item struct {
	DiskExpiration   int64
	MemoryExpiration int64
	Data             []byte
	Handle           *os.File
}

func (i *Item) Read() ([]byte, error) {
	if i.MemoryExpiration == -1 {
		data, err := i.readFile()
		if err != nil {
			return nil, fmt.Errorf("failed to read file from disk: %w", err)
		}
		i.Data = data
	}

	return i.Data, nil
}

func (i *Item) readFile() ([]byte, error) {
	return io.ReadAll(i.Handle)
}

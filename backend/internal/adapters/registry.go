package adapters

import (
	"sort"
	"sync"
)

var (
	mu       sync.RWMutex
	registry = map[string]Adapter{}
)

func Register(a Adapter) {
	mu.Lock()
	defer mu.Unlock()
	registry[a.Kind()] = a
}

func Get(kind string) (Adapter, bool) {
	mu.RLock()
	defer mu.RUnlock()
	a, ok := registry[kind]
	return a, ok
}

func Kinds() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

package persistent

import (
	"study-go.ru/cho/eto/pkg/store"
)

func Lookup(s store.Store, key string) ([]byte, error) {
	// ...
	return s.Get(key)
}

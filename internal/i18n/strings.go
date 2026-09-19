package i18n

import (
	"fmt"
	"strings"
	"sync"
)

type lang struct {
	data map[string]string
}

var (
	mu         sync.RWMutex
	cur        *lang
	curCode    = "en"
	allLangs   = map[string]*lang{}
	langOrder  = []string{"en", "tr", "de", "fr", "es", "it", "pt", "ru", "ja"}
	prefGetter func() string
	prefSetter func(string)
)

func RegisterPrefs(get func() string, set func(string)) {
	prefGetter = get
	prefSetter = set
}

func Init() {
	curCode = "en"
	if prefGetter != nil {
		if c := prefGetter(); c != "" {
			if l, ok := allLangs[c]; ok {
				cur = l
				curCode = c
				return
			}
		}
	}
	if l, ok := allLangs["en"]; ok {
		cur = l
	}
}

func SetLang(code string) {
	mu.Lock()
	defer mu.Unlock()
	if l, ok := allLangs[code]; ok {
		cur = l
		curCode = code
		if prefSetter != nil {
			prefSetter(code)
		}
	}
}

func CurrentCode() string {
	mu.RLock()
	defer mu.RUnlock()
	return curCode
}

func Available() []string {
	return append([]string{}, langOrder...)
}

func T(key string, args ...interface{}) string {
	mu.RLock()
	l := cur
	mu.RUnlock()
	if l == nil {
		return key
	}
	s, ok := l.data[key]
	if !ok {
		fb, ok := allLangs["en"]
		if ok {
			s, ok = fb.data[key]
		}
		if !ok {
			return key
		}
	}
	if len(args) > 0 {
		if m, ok := args[0].(map[string]interface{}); ok {
			for k, v := range m {
				s = strings.ReplaceAll(s, "{"+k+"}", fmt.Sprintf("%v", v))
			}
		}
	}
	return s
}

func reg(code string, data map[string]string) {
	if existing, ok := allLangs[code]; ok {
		for k, v := range data {
			existing.data[k] = v
		}
	} else {
		allLangs[code] = &lang{data: data}
	}
}

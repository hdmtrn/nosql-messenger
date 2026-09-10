package main

import (
	"net/http/httptest"
	"testing"
)

func TestCheckOrigin(t *testing.T) {
	// extraOrigins читается из окружения при инициализации пакета, поэтому в
	// тесте проще подменить переменную, чем переинициализировать пакет.
	extraOrigins = []string{"http://localhost:5173"}

	cases := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{"без Origin — не браузер, пропускаем", "example.com", "", true},
		{"свой хост", "example.com", "https://example.com", true},
		{"свой хост в другом регистре", "example.com", "https://EXAMPLE.com", true},
		{"свой хост с портом", "localhost:8080", "http://localhost:8080", true},
		{"чужой хост", "example.com", "https://evil.com", false},
		{"поддомен — это чужой хост", "example.com", "https://a.example.com", false},
		{"null из песочницы", "example.com", "null", false},
		{"дев-сервер из списка исключений", "localhost:8080", "http://localhost:5173", true},
		{"тот же хост, но другая схема — точное сравнение", "localhost:8080", "https://localhost:5173", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/ws", nil)
			r.Host = c.host
			if c.origin != "" {
				r.Header.Set("Origin", c.origin)
			}
			if got := checkOrigin(r); got != c.want {
				t.Errorf("checkOrigin(Host=%q, Origin=%q) = %v, ожидалось %v",
					c.host, c.origin, got, c.want)
			}
		})
	}
}

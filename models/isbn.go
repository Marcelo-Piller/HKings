package models

import (
	"encoding/json"
	"strings"

	"github.com/vendas/loja/database"
)

// NormalizeISBN aceita ISBN-10 ou ISBN-13 com ou sem hífens e espaços,
// confere o dígito verificador e devolve sempre o ISBN-13 só com dígitos.
func NormalizeISBN(raw string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(raw) {
		switch {
		case r >= '0' && r <= '9', r == 'X':
			b.WriteRune(r)
		case r == '-', r == ' ', r == '.', r == ' ':
		default:
			return "", false
		}
	}
	s := b.String()
	switch len(s) {
	case 10:
		sum := 0
		for i := 0; i < 10; i++ {
			c := s[i]
			var v int
			switch {
			case c == 'X' && i == 9:
				v = 10
			case c >= '0' && c <= '9':
				v = int(c - '0')
			default:
				return "", false
			}
			sum += v * (10 - i)
		}
		if sum%11 != 0 {
			return "", false
		}
		return isbn13From10(s[:9]), true
	case 13:
		if strings.ContainsRune(s, 'X') || (!strings.HasPrefix(s, "978") && !strings.HasPrefix(s, "979")) {
			return "", false
		}
		if isbn13Check(s[:12]) != s[12] {
			return "", false
		}
		return s, true
	}
	return "", false
}

func isbn13Check(first12 string) byte {
	sum := 0
	for i := 0; i < 12; i++ {
		v := int(first12[i] - '0')
		if i%2 == 1 {
			v *= 3
		}
		sum += v
	}
	return byte('0' + (10-sum%10)%10)
}

func isbn13From10(first9 string) string {
	base := "978" + first9
	return base + string(isbn13Check(base))
}

// BookMetadata é o que a consulta por ISBN encontrou.
type BookMetadata struct {
	Title       string `json:"titulo"`
	Authors     string `json:"autor"`
	Publisher   string `json:"editora"`
	Language    string `json:"idioma"`
	Description string `json:"descricao"`
	CoverURL    string `json:"capa"`
	Pages       int    `json:"paginas,omitempty"`
}

// Empty diz se a consulta não trouxe nada útil.
func (m BookMetadata) Empty() bool {
	return m.Title == "" && m.Authors == "" && m.CoverURL == ""
}

// CachedISBN devolve a consulta guardada. found=false com ok=true é um ISBN
// que já foi procurado e não existe nas bases (consultado há menos de 7 dias).
func CachedISBN(isbn string) (meta BookMetadata, found, ok bool) {
	var data string
	var f int
	err := database.DB.QueryRow(`SELECT data_json, found FROM isbn_cache
		WHERE isbn=? AND (found=1 OR fetched_at > datetime('now','-7 days'))`, isbn).Scan(&data, &f)
	if err != nil {
		return meta, false, false
	}
	json.Unmarshal([]byte(data), &meta)
	return meta, f == 1, true
}

// CacheISBN guarda o resultado da consulta.
func CacheISBN(isbn string, meta BookMetadata, found bool) {
	data, _ := json.Marshal(meta)
	database.DB.Exec(`INSERT OR REPLACE INTO isbn_cache (isbn, data_json, found, fetched_at) VALUES (?,?,?,CURRENT_TIMESTAMP)`,
		isbn, string(data), boolToInt(found))
}

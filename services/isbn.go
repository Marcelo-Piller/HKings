package services

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/vendas/loja/models"
)

// Consulta de livros por ISBN: Google Books (título, autor, editora, idioma,
// sinopse) e Open Library (capa em boa resolução e o que faltar). Os
// resultados ficam no cache (models.CachedISBN), inclusive "não encontrado".

var isbnClient = &http.Client{Timeout: 8 * time.Second}

func googleBooksURL() string {
	if u := os.Getenv("ISBN_GOOGLE_URL"); u != "" {
		return u
	}
	return "https://www.googleapis.com/books/v1/volumes"
}

func openLibraryURL() string {
	if u := os.Getenv("ISBN_OPENLIBRARY_URL"); u != "" {
		return u
	}
	return "https://openlibrary.org/api/books"
}

// LookupISBN busca os dados do livro. isbn precisa vir normalizado
// (models.NormalizeISBN). found=false quando nenhuma base conhece o livro.
func LookupISBN(isbn string) (models.BookMetadata, bool, error) {
	if meta, found, ok := models.CachedISBN(isbn); ok {
		return meta, found, nil
	}
	meta, errG := googleBooks(isbn)
	ol, errO := openLibrary(isbn)
	if errG != nil && errO != nil {
		// As duas bases fora do ar: não grava "não encontrado" no cache.
		return meta, false, fmt.Errorf("consulta de ISBN indisponível: %v", errG)
	}
	if errG != nil {
		log.Printf("Google Books: %v (configure a chave em Configurações)", errG)
	}
	// Open Library completa o que faltou; a capa dela é maior que a do Google.
	if ol.CoverURL != "" {
		meta.CoverURL = ol.CoverURL
	}
	fill := func(dst *string, src string) {
		if *dst == "" {
			*dst = src
		}
	}
	fill(&meta.Title, ol.Title)
	fill(&meta.Authors, ol.Authors)
	fill(&meta.Publisher, ol.Publisher)
	if meta.Pages == 0 {
		meta.Pages = ol.Pages
	}
	found := !meta.Empty()
	// Com uma base fora do ar, só guarda o que achou: "não encontrado" ou um
	// resultado parcial pode melhorar quando ela voltar.
	if errG == nil && errO == nil || found && errG == nil {
		models.CacheISBN(isbn, meta, found)
	}
	return meta, found, nil
}

var langNames = map[string]string{
	"pt": "Português", "pt-br": "Português", "pt-pt": "Português", "en": "Inglês", "es": "Espanhol",
	"fr": "Francês", "it": "Italiano", "de": "Alemão", "ja": "Japonês", "ko": "Coreano", "zh": "Chinês",
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func cleanText(s string, max int) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = strings.NewReplacer("&quot;", `"`, "&amp;", "&", "&#39;", "'", "&lt;", "<", "&gt;", ">").Replace(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	s = strings.TrimSpace(strings.Join(lines, "\n"))
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max-1])) + "…"
	}
	return s
}

func getJSON(u string, out any) error {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Livredo/1.0 (+https://livredo.com.br)")
	resp, err := isbnClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
}

func googleBooks(isbn string) (models.BookMetadata, error) {
	q := url.Values{"q": {"isbn:" + isbn}, "maxResults": {"1"}}
	if key := models.GetSetting("google_books_api_key"); key != "" {
		q.Set("key", key)
	}
	var res struct {
		Items []struct {
			VolumeInfo struct {
				Title       string   `json:"title"`
				Subtitle    string   `json:"subtitle"`
				Authors     []string `json:"authors"`
				Publisher   string   `json:"publisher"`
				Language    string   `json:"language"`
				Description string   `json:"description"`
				PageCount   int      `json:"pageCount"`
				ImageLinks  struct {
					Thumbnail string `json:"thumbnail"`
				} `json:"imageLinks"`
			} `json:"volumeInfo"`
		} `json:"items"`
	}
	if err := getJSON(googleBooksURL()+"?"+q.Encode(), &res); err != nil {
		return models.BookMetadata{}, err
	}
	if len(res.Items) == 0 {
		return models.BookMetadata{}, nil
	}
	v := res.Items[0].VolumeInfo
	meta := models.BookMetadata{
		Title:       cleanText(v.Title, 120),
		Authors:     cleanText(strings.Join(v.Authors, ", "), 120),
		Publisher:   cleanText(v.Publisher, 120),
		Description: cleanText(v.Description, 2000),
		Pages:       v.PageCount,
	}
	if name, ok := langNames[strings.ToLower(v.Language)]; ok {
		meta.Language = name
	}
	if t := v.ImageLinks.Thumbnail; t != "" {
		t = strings.Replace(t, "http://", "https://", 1)
		t = strings.Replace(t, "&edge=curl", "", 1)
		meta.CoverURL = t
	}
	return meta, nil
}

func openLibrary(isbn string) (models.BookMetadata, error) {
	q := url.Values{"bibkeys": {"ISBN:" + isbn}, "format": {"json"}, "jscmd": {"data"}}
	var res map[string]struct {
		Title   string `json:"title"`
		Authors []struct {
			Name string `json:"name"`
		} `json:"authors"`
		Publishers []struct {
			Name string `json:"name"`
		} `json:"publishers"`
		Pages int `json:"number_of_pages"`
		Cover struct {
			Large  string `json:"large"`
			Medium string `json:"medium"`
		} `json:"cover"`
	}
	if err := getJSON(openLibraryURL()+"?"+q.Encode(), &res); err != nil {
		return models.BookMetadata{}, err
	}
	b, ok := res["ISBN:"+isbn]
	if !ok {
		return models.BookMetadata{}, nil
	}
	var authors []string
	for _, a := range b.Authors {
		authors = append(authors, a.Name)
	}
	meta := models.BookMetadata{
		Title:   cleanText(b.Title, 120),
		Authors: cleanText(strings.Join(authors, ", "), 120),
		Pages:   b.Pages,
	}
	if len(b.Publishers) > 0 {
		meta.Publisher = cleanText(b.Publishers[0].Name, 120)
	}
	cover := b.Cover.Large
	if cover == "" {
		cover = b.Cover.Medium
	}
	meta.CoverURL = strings.Replace(cover, "http://", "https://", 1)
	return meta, nil
}

package models

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/nwaples/rardecode/v2"
)

// Quadrinhos digitais: CBR (RAR) e CBZ (ZIP) são pacotes de imagens, uma por
// página, na ordem dos nomes dos arquivos (001.jpg, 002.jpg…).

// maxComicPageBytes limita cada imagem lida do pacote (contra zip bomb).
const maxComicPageBytes = 30 << 20

// rarDictLimit limita a memória usada para descompactar RAR.
const rarDictLimit = 256 << 20

var comicImageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true}

// IsComicType diz se o tipo é de quadrinho (pacote de imagens).
func IsComicType(t string) bool { return t == EbookCBR || t == EbookCBZ }

func isComicImage(name string) bool {
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	if strings.HasPrefix(base, ".") || strings.Contains(name, "__MACOSX") {
		return false
	}
	return comicImageExts[strings.ToLower(path.Ext(base))]
}

var comicCache = struct {
	sync.Mutex
	m map[string][]string
}{m: map[string][]string{}}

// ComicPages lista as imagens do quadrinho na ordem de leitura.
func ComicPages(file, kind string) ([]string, error) {
	st, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%s|%d|%d", file, st.Size(), st.ModTime().UnixNano())
	comicCache.Lock()
	if pages, ok := comicCache.m[key]; ok {
		comicCache.Unlock()
		return pages, nil
	}
	comicCache.Unlock()

	var names []string
	switch kind {
	case EbookCBZ:
		zr, err := zip.OpenReader(file)
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if !f.FileInfo().IsDir() && isComicImage(f.Name) {
				names = append(names, f.Name)
			}
		}
		zr.Close()
	case EbookCBR:
		rr, err := rardecode.OpenReader(file, rardecode.MaxDictionarySize(rarDictLimit))
		if err != nil {
			return nil, err
		}
		for {
			h, err := rr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				rr.Close()
				return nil, err
			}
			if !h.IsDir && isComicImage(h.Name) {
				names = append(names, h.Name)
			}
		}
		rr.Close()
	default:
		return nil, ErrInvalidEbook
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("quadrinho sem imagens")
	}
	sort.SliceStable(names, func(i, j int) bool { return naturalLess(names[i], names[j]) })
	comicCache.Lock()
	if len(comicCache.m) > 64 {
		comicCache.m = map[string][]string{}
	}
	comicCache.m[key] = names
	comicCache.Unlock()
	return names, nil
}

// ReadComicPage devolve a imagem da página n (1 = primeira).
func ReadComicPage(file, kind string, n int) ([]byte, error) {
	pages, err := ComicPages(file, kind)
	if err != nil {
		return nil, err
	}
	if n < 1 || n > len(pages) {
		return nil, fmt.Errorf("página %d fora do quadrinho", n)
	}
	want := pages[n-1]
	read := func(r io.Reader) ([]byte, error) {
		data, err := io.ReadAll(io.LimitReader(r, maxComicPageBytes+1))
		if err != nil {
			return nil, err
		}
		if len(data) > maxComicPageBytes {
			return nil, fmt.Errorf("página grande demais")
		}
		return data, nil
	}
	if kind == EbookCBZ {
		zr, err := zip.OpenReader(file)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		for _, f := range zr.File {
			if f.Name == want {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return read(rc)
			}
		}
		return nil, fmt.Errorf("página não encontrada")
	}
	rr, err := rardecode.OpenReader(file, rardecode.MaxDictionarySize(rarDictLimit))
	if err != nil {
		return nil, err
	}
	defer rr.Close()
	for {
		h, err := rr.Next()
		if err != nil {
			return nil, fmt.Errorf("página não encontrada: %w", err)
		}
		if h.Name == want && !h.IsDir {
			return read(rr)
		}
	}
}

// naturalLess compara nomes com números pelo valor: "pag2" antes de "pag10".
func naturalLess(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		ra, rb := rune(a[0]), rune(b[0])
		if unicode.IsDigit(ra) && unicode.IsDigit(rb) {
			na, restA := leadingDigits(a)
			nb, restB := leadingDigits(b)
			na, nb = strings.TrimLeft(na, "0"), strings.TrimLeft(nb, "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			a, b = restA, restB
			continue
		}
		if ra != rb {
			return ra < rb
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func leadingDigits(s string) (string, string) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i], s[i:]
}

package models

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"github.com/vendas/loja/database"
)

// Importação de catálogo por planilha (CSV). O lojista envia a planilha, o
// site completa o que der pelo ISBN, valida cada linha e mostra uma prévia;
// só depois de confirmar os livros entram na loja. Linha de livro que o
// lojista já tem (mesmo ISBN ou coluna id) atualiza só preço e estoque.
//
// Só livros físicos: e-book exige o arquivo e a declaração de direitos, que
// continuam no formulário de cadastro.

// MaxImportRows limita as linhas de uma planilha.
const MaxImportRows = 1000

// MaxImportBytes limita o arquivo enviado.
const MaxImportBytes = 2 << 20

// ImportColumns são as colunas do modelo de planilha, na ordem.
var ImportColumns = []string{"id", "isbn", "titulo", "autor", "editora", "idioma", "categoria", "estado", "preco", "estoque",
	"descricao", "capa_url", "peso_g", "altura_cm", "largura_cm", "comprimento_cm"}

// importAliases aceita nomes de coluna comuns além dos do modelo.
var importAliases = map[string]string{
	"codigo": "id", "titulo": "titulo", "nome": "titulo", "livro": "titulo", "title": "titulo",
	"autora": "autor", "autores": "autor", "author": "autor",
	"publisher": "editora", "lingua": "idioma", "language": "idioma",
	"genero": "categoria", "category": "categoria", "condicao": "estado", "conservacao": "estado",
	"preco": "preco", "valor": "preco", "price": "preco", "quantidade": "estoque", "qtd": "estoque", "qtde": "estoque", "stock": "estoque",
	"descricao": "descricao", "sinopse": "descricao", "description": "descricao",
	"capa": "capa_url", "imagem": "capa_url", "foto": "capa_url", "url_capa": "capa_url", "link_capa": "capa_url", "image": "capa_url",
	"peso": "peso_g", "altura": "altura_cm", "largura": "largura_cm", "comprimento": "comprimento_cm",
	"isbn13": "isbn", "isbn10": "isbn", "ean": "isbn", "codigo_de_barras": "isbn",
}

// ImportRow é uma linha da planilha, como lida e como vai ser publicada.
type ImportRow struct {
	Line        int
	ID          int // coluna id: livro já cadastrado
	ISBN        string
	Title       string
	Author      string
	Publisher   string
	Language    string
	Category    string
	Condition   string
	PriceText   string
	Price       float64
	StockText   string
	Stock       int
	Description string
	CoverURL    string
	WeightG     int
	HeightCm    int
	WidthCm     int
	LengthCm    int

	// FromISBN são os campos completados pela consulta do ISBN.
	FromISBN []string
	// ParseErrors são os erros da leitura (preço, ISBN, medidas); Errors
	// junta esses aos da validação.
	ParseErrors []string
	Notes       []string // avisos do processamento (ISBN não encontrado)
	Errors      []string
	Warnings    []string
	// UpdateID é o livro do lojista que esta linha atualiza (preço e estoque).
	UpdateID int

	// Resultado da publicação: "publicado", "atualizado" ou "falhou".
	Status string
	Result string
}

// Ready diz se a linha pode ser publicada.
func (r ImportRow) Ready() bool { return len(r.Errors) == 0 }

// Filled diz se o campo veio da consulta do ISBN.
func (r ImportRow) Filled(field string) bool {
	for _, f := range r.FromISBN {
		if f == field {
			return true
		}
	}
	return false
}

// NeedsLookup diz se vale consultar o ISBN: falta algum dado que ele traz.
func (r ImportRow) NeedsLookup() bool {
	if r.ISBN == "" || r.ID > 0 {
		return false
	}
	return r.Title == "" || r.Author == "" || r.Publisher == "" || r.Language == "" || r.CoverURL == "" || r.Description == ""
}

// ApplyMetadata completa os campos vazios com o que o ISBN trouxe.
func (r *ImportRow) ApplyMetadata(m BookMetadata) {
	fill := func(dst *string, src, field string) {
		if *dst == "" && src != "" {
			*dst = src
			r.FromISBN = append(r.FromISBN, field)
		}
	}
	fill(&r.Title, m.Title, "titulo")
	fill(&r.Author, m.Authors, "autor")
	fill(&r.Publisher, m.Publisher, "editora")
	fill(&r.Language, m.Language, "idioma")
	fill(&r.Description, m.Description, "descricao")
	fill(&r.CoverURL, m.CoverURL, "capa")
}

// headerKey normaliza o nome da coluna: sem acentos, minúsculo, com "_".
func headerKey(h string) string {
	k := strings.ReplaceAll(AuthorSlug(h), "-", "_")
	if a, ok := importAliases[k]; ok {
		return a
	}
	return k
}

// ParseImportCSV lê a planilha. Aceita vírgula ou ponto e vírgula (o Excel em
// português salva com ";"), UTF-8 com ou sem BOM e Windows-1252 (Excel antigo).
func ParseImportCSV(data []byte) ([]ImportRow, error) {
	if len(data) > MaxImportBytes {
		return nil, fmt.Errorf("a planilha passa de %d MB; divida em arquivos menores", MaxImportBytes>>20)
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		dec, err := charmap.Windows1252.NewDecoder().Bytes(data)
		if err != nil {
			return nil, errors.New("não foi possível ler a planilha; salve como CSV UTF-8")
		}
		data = dec
	}
	firstLine := string(data)
	if i := strings.IndexAny(firstLine, "\r\n"); i >= 0 {
		firstLine = firstLine[:i]
	}
	sep := ','
	if strings.Count(firstLine, ";") > strings.Count(firstLine, ",") {
		sep = ';'
	} else if strings.Count(firstLine, "\t") > strings.Count(firstLine, ",") {
		sep = '\t'
	}
	rd := csv.NewReader(bytes.NewReader(data))
	rd.Comma = sep
	rd.FieldsPerRecord = -1
	rd.LazyQuotes = true
	header, err := rd.Read()
	if err != nil {
		return nil, errors.New("a planilha está vazia")
	}
	cols := map[string]int{}
	for i, h := range header {
		if k := headerKey(h); k != "" {
			if _, dup := cols[k]; !dup {
				cols[k] = i
			}
		}
	}
	if _, ok := cols["titulo"]; !ok {
		if _, ok := cols["isbn"]; !ok {
			return nil, errors.New("a planilha precisa ter pelo menos a coluna \"titulo\" ou \"isbn\" na primeira linha (use o modelo)")
		}
	}
	if _, ok := cols["preco"]; !ok {
		return nil, errors.New("a planilha precisa da coluna \"preco\" (use o modelo)")
	}

	var rows []ImportRow
	line := 1
	for {
		rec, err := rd.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line++
		if err != nil {
			return nil, fmt.Errorf("linha %d: %v", line, err)
		}
		get := func(k string) string {
			if i, ok := cols[k]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		empty := true
		for _, v := range rec {
			if strings.TrimSpace(v) != "" {
				empty = false
				break
			}
		}
		if empty {
			continue
		}
		if len(rows) >= MaxImportRows {
			return nil, fmt.Errorf("a planilha tem mais de %d livros; divida em arquivos menores", MaxImportRows)
		}
		r := ImportRow{
			Line: line, Title: get("titulo"), Author: get("autor"), Publisher: get("editora"), Language: get("idioma"),
			Category: get("categoria"), Condition: get("estado"), PriceText: get("preco"), StockText: get("estoque"),
			Description: get("descricao"), CoverURL: get("capa_url"),
		}
		r.Title = strings.Join(strings.Fields(r.Title), " ")
		r.Author = strings.Join(strings.Fields(r.Author), " ")
		if v := get("id"); v != "" {
			id, err := strconv.Atoi(v)
			if err != nil || id <= 0 {
				r.ParseErrors = append(r.ParseErrors, "id inválido: deixe em branco para livro novo")
			}
			r.ID = id
		}
		if v := get("isbn"); v != "" {
			if isbn, ok := NormalizeISBN(v); ok {
				r.ISBN = isbn
			} else {
				r.ParseErrors = append(r.ParseErrors, "ISBN inválido: confira os dígitos")
			}
		}
		r.Price, err = ParsePriceBR(r.PriceText)
		if err != nil {
			r.ParseErrors = append(r.ParseErrors, err.Error())
		}
		r.Stock = 1
		if r.StockText != "" {
			n, err := strconv.Atoi(strings.TrimSpace(strings.ReplaceAll(r.StockText, ".", "")))
			if err != nil || n < 0 || n > 1_000_000 {
				r.ParseErrors = append(r.ParseErrors, "estoque inválido: use um número inteiro")
			}
			r.Stock = n
		}
		for k, dst := range map[string]*int{"peso_g": &r.WeightG, "altura_cm": &r.HeightCm, "largura_cm": &r.WidthCm, "comprimento_cm": &r.LengthCm} {
			if v := get(k); v != "" {
				f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
				if err != nil || f < 0 {
					r.ParseErrors = append(r.ParseErrors, k+" inválido")
					continue
				}
				*dst = int(math.Round(f))
			}
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return nil, errors.New("nenhum livro encontrado na planilha")
	}
	return rows, nil
}

// ParsePriceBR entende "39,90", "R$ 1.234,56", "39.90" e "1,234.56".
func ParsePriceBR(s string) (float64, error) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "R$"))
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return 0, errors.New("preço em branco")
	}
	lastComma, lastDot := strings.LastIndex(s, ","), strings.LastIndex(s, ".")
	switch {
	case lastComma > lastDot: // 1.234,56 ou 39,90
		s = strings.ReplaceAll(s, ".", "")
		s = strings.Replace(s, ",", ".", 1)
	case lastDot > lastComma && lastComma >= 0: // 1,234.56
		s = strings.ReplaceAll(s, ",", "")
	case lastDot >= 0 && len(s)-lastDot-1 == 3 && strings.Count(s, ".") >= 1 && !strings.Contains(s[:lastDot], "."):
		// "1.234" sem centavos: milhar brasileiro.
		s = strings.ReplaceAll(s, ".", "")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 || v > 1_000_000 || math.IsNaN(v) {
		return 0, errors.New("preço inválido: use, por exemplo, 39,90")
	}
	return math.Round(v*100) / 100, nil
}

// conditionFromText entende "novo", "usado", "seminovo", "N", "U".
func conditionFromText(s string) string {
	switch AuthorSlug(s) {
	case "novo", "n", "nova", "lacrado", "new":
		return ConditionNew
	case "usado", "u", "usada", "seminovo", "semi-novo", "seminova", "sebo", "used":
		return ConditionUsed
	}
	return ""
}

// matchCategory acha a categoria cadastrada sem diferença de acento ou
// maiúscula: "ficcao" encontra "Ficção".
func matchCategory(name string, categories []string) string {
	slug := AuthorSlug(name)
	for _, c := range categories {
		if AuthorSlug(c) == slug {
			return c
		}
	}
	return ""
}

// ValidateImportRows confere cada linha antes da publicação e marca as que
// atualizam livros já cadastrados do lojista (ownerID 0 = o próprio site).
func ValidateImportRows(rows []ImportRow, ownerID int) {
	cats := []string{}
	if list, err := ListCategoriesFull(); err == nil {
		for _, c := range list {
			cats = append(cats, c.Name)
		}
	}
	owned := map[int]bool{}
	byISBN := map[string]int{}
	if mine, err := ListProductsBySeller(ownerID); err == nil {
		for _, p := range mine {
			if p.IsDigital() {
				continue
			}
			owned[p.ID] = true
			if p.ISBN != "" {
				byISBN[p.ISBN] = p.ID
			}
		}
	}
	seenISBN := map[string]int{}
	for i := range rows {
		r := &rows[i]
		r.Warnings = append([]string(nil), r.Notes...)
		r.Errors = append([]string(nil), r.ParseErrors...)

		r.UpdateID = 0
		if r.ID > 0 {
			if owned[r.ID] {
				r.UpdateID = r.ID
			} else {
				r.Errors = append(r.Errors, fmt.Sprintf("id %d não é um livro físico seu", r.ID))
			}
		} else if id, ok := byISBN[r.ISBN]; ok && r.ISBN != "" {
			r.UpdateID = id
		}
		if r.ISBN != "" {
			if first, dup := seenISBN[r.ISBN]; dup {
				r.Errors = append(r.Errors, fmt.Sprintf("ISBN repetido na planilha (linha %d): some as quantidades numa linha só", first))
			} else {
				seenISBN[r.ISBN] = r.Line
			}
		}
		if r.UpdateID > 0 {
			r.Warnings = append(r.Warnings, fmt.Sprintf("Já cadastrado (livro %d): só preço e estoque serão atualizados.", r.UpdateID))
			continue
		}

		if r.Title == "" {
			if r.ISBN != "" {
				r.Errors = append(r.Errors, "título em branco e o ISBN não foi encontrado nas bases")
			} else {
				r.Errors = append(r.Errors, "título em branco")
			}
		} else if len([]rune(r.Title)) > 120 {
			r.Errors = append(r.Errors, "o título passa de 120 caracteres")
		}
		if r.Category == "" {
			r.Errors = append(r.Errors, "categoria em branco")
		} else if c := matchCategory(r.Category, cats); c != "" {
			r.Category = c
		} else {
			r.Errors = append(r.Errors, fmt.Sprintf("categoria %q não existe na loja", r.Category))
		}
		if c := conditionFromText(r.Condition); c != "" {
			r.Condition = c
		} else if r.Condition == "" {
			r.Errors = append(r.Errors, "estado em branco: novo ou usado")
		} else {
			r.Errors = append(r.Errors, fmt.Sprintf("estado %q inválido: use novo ou usado", r.Condition))
		}
		if len([]rune(r.Author)) > 120 {
			r.Author = string([]rune(r.Author)[:120])
		}
		if len([]rune(r.Description)) > 2000 {
			r.Description = strings.TrimSpace(string([]rune(r.Description)[:1999])) + "…"
		}
		if r.CoverURL != "" && !strings.HasPrefix(r.CoverURL, "http://") && !strings.HasPrefix(r.CoverURL, "https://") {
			r.Errors = append(r.Errors, "capa_url precisa começar com http:// ou https://")
		}
		if r.Ready() {
			p := r.Product(ownerID, placeholderForValidation)
			if err := validateProduct(&p); err != nil {
				msg := err.Error()
				if _, d, ok := strings.Cut(msg, ": "); ok {
					msg = d
				}
				r.Errors = append(r.Errors, msg)
			}
		}
		if r.Author == "" {
			r.Warnings = append(r.Warnings, "Sem autor.")
		}
		if r.CoverURL == "" {
			r.Warnings = append(r.Warnings, "Sem capa: a loja gera uma capa com o título.")
		}
		if r.Description == "" {
			r.Warnings = append(r.Warnings, "Sem sinopse.")
		}
	}
}

const placeholderForValidation = "/static/img/placeholder.svg"

// Product monta o livro que a linha cria.
func (r ImportRow) Product(ownerID int, imageURL string) Product {
	return Product{
		SellerID: ownerID, Name: r.Title, Description: r.Description, Price: r.Price, Stock: r.Stock,
		ImageURL: imageURL, Category: r.Category, Condition: r.Condition, Active: true, Format: FormatPhysical,
		Author: r.Author, ISBN: r.ISBN, Publisher: r.Publisher, Language: r.Language,
		WeightG: r.WeightG, HeightCm: r.HeightCm, WidthCm: r.WidthCm, LengthCm: r.LengthCm,
	}
}

// UpdatePriceStock atualiza preço e estoque de um livro físico do dono.
func UpdatePriceStock(id, ownerID int, price float64, stock int) error {
	if price <= 0 || price > 1_000_000 || stock < 0 || stock > 1_000_000 {
		return ErrInvalidProduct
	}
	res, err := database.DB.Exec(`UPDATE products SET price=?, stock=? WHERE id=? AND seller_id=? AND archived=0 AND product_format<>'digital'`,
		math.Round(price*100)/100, stock, id, ownerID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrInvalidProduct
	}
	return nil
}

// ── Lotes ────────────────────────────────────────────────────────────────────

// Situação do lote de importação.
const (
	ImportProcessing = "processando" // consultando ISBNs
	ImportReady      = "pronto"      // prévia, esperando confirmação
	ImportPublishing = "publicando"
	ImportDone       = "concluido"
)

// ImportBatch é uma planilha enviada.
type ImportBatch struct {
	ID        string
	SellerID  int
	Status    string
	Done      int
	Total     int
	Rows      []ImportRow
	CreatedAt string
	Stale     bool // processamento interrompido (o servidor reiniciou)
}

// Counts resume a prévia: novos, atualizações e linhas com erro.
func (b ImportBatch) Counts() (novos, atualizar, erros int) {
	for _, r := range b.Rows {
		switch {
		case !r.Ready():
			erros++
		case r.UpdateID > 0:
			atualizar++
		default:
			novos++
		}
	}
	return
}

// Novos, Atualizar e ComErro são os totais da prévia, para o template.
func (b ImportBatch) Novos() int     { n, _, _ := b.Counts(); return n }
func (b ImportBatch) Atualizar() int { _, a, _ := b.Counts(); return a }
func (b ImportBatch) ComErro() int   { _, _, e := b.Counts(); return e }

// Publicaveis são as linhas que a publicação vai gravar.
func (b ImportBatch) Publicaveis() int { n, a, _ := b.Counts(); return n + a }

// Resultado conta as linhas pela situação da publicação.
func (b ImportBatch) Resultado(status string) int {
	n := 0
	for _, r := range b.Rows {
		if r.Status == status {
			n++
		}
	}
	return n
}

// Percent é o progresso do processamento.
func (b ImportBatch) Percent() int {
	if b.Total == 0 {
		return 0
	}
	return b.Done * 100 / b.Total
}

// CreateImportBatch guarda a planilha lida e apaga lotes com mais de 3 dias.
func CreateImportBatch(ownerID int, rows []ImportRow) (string, error) {
	database.DB.Exec(`DELETE FROM import_batches WHERE created_at < datetime('now','-3 days')`)
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw[:])
	data, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	_, err = database.DB.Exec(`INSERT INTO import_batches (id, seller_id, status, done, total, rows_json) VALUES (?,?,?,?,?,?)`,
		id, ownerID, ImportProcessing, 0, len(rows), string(data))
	return id, err
}

// GetImportBatch devolve o lote, só para o dono.
func GetImportBatch(id string, ownerID int) (*ImportBatch, error) {
	b := &ImportBatch{ID: id}
	var data string
	var stale int
	err := database.DB.QueryRow(`SELECT seller_id, status, done, total, rows_json, created_at,
			created_at < datetime('now','-30 minutes')
		FROM import_batches WHERE id=? AND seller_id=?`, id, ownerID).
		Scan(&b.SellerID, &b.Status, &b.Done, &b.Total, &data, &b.CreatedAt, &stale)
	if err != nil {
		return nil, err
	}
	b.Stale = stale == 1 && (b.Status == ImportProcessing || b.Status == ImportPublishing)
	if err := json.Unmarshal([]byte(data), &b.Rows); err != nil {
		return nil, err
	}
	return b, nil
}

// SaveImportBatch grava as linhas e a situação do lote.
func SaveImportBatch(b *ImportBatch) error {
	data, err := json.Marshal(b.Rows)
	if err != nil {
		return err
	}
	_, err = database.DB.Exec(`UPDATE import_batches SET status=?, done=?, total=?, rows_json=? WHERE id=?`,
		b.Status, b.Done, b.Total, string(data), b.ID)
	return err
}

// SetImportProgress grava só o progresso, durante o processamento.
func SetImportProgress(id string, done, total int) {
	database.DB.Exec(`UPDATE import_batches SET done=?, total=? WHERE id=?`, done, total, id)
}

// StartImportPublish passa o lote de "pronto" a "publicando"; false se outro
// clique já começou a publicação.
func StartImportPublish(id string, ownerID int) (bool, error) {
	res, err := database.DB.Exec(`UPDATE import_batches SET status=?, done=0, created_at=CURRENT_TIMESTAMP
		WHERE id=? AND seller_id=? AND status=?`, ImportPublishing, id, ownerID, ImportReady)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ErrNoBatch: lote inexistente ou de outro dono.
var ErrNoBatch = sql.ErrNoRows

package handlers

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/vendas/loja/models"
	"github.com/vendas/loja/services"
)

// Catálogo em massa, igual nos painéis do vendedor e do admin: importação
// por planilha (com prévia e busca por ISBN), exportação do catálogo,
// edição em massa e a busca de ISBN do formulário de livro.

// catalogOwner é de quem são os livros: o vendedor logado ou o próprio site
// (admin, dono 0).
type catalogOwner struct {
	ID     int
	Base   string // "/vendedor" ou "/admin"
	render func(w http.ResponseWriter, r *http.Request, page string, data map[string]any)
	page   string // prefixo dos templates: "seller_" ou "admin_"
}

func sellerOwner(r *http.Request) catalogOwner {
	seller := currentSeller(r)
	return catalogOwner{ID: seller.ID, Base: "/vendedor", page: "seller_",
		render: func(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
			data["Seller"] = seller
			renderSeller(w, r, page, data)
		}}
}

func adminOwner(*http.Request) catalogOwner {
	return catalogOwner{ID: 0, Base: "/admin", page: "admin_", render: renderAdmin}
}

// Rotas: uma por painel, a mesma lógica.
func SellerImport(w http.ResponseWriter, r *http.Request)        { importPage(sellerOwner(r), w, r) }
func AdminImport(w http.ResponseWriter, r *http.Request)         { importPage(adminOwner(r), w, r) }
func SellerImportPublish(w http.ResponseWriter, r *http.Request) { importPublish(sellerOwner(r), w, r) }
func AdminImportPublish(w http.ResponseWriter, r *http.Request)  { importPublish(adminOwner(r), w, r) }
func SellerCatalogExport(w http.ResponseWriter, r *http.Request) { catalogExport(sellerOwner(r), w, r) }
func AdminCatalogExport(w http.ResponseWriter, r *http.Request)  { catalogExport(adminOwner(r), w, r) }
func SellerBulkEdit(w http.ResponseWriter, r *http.Request)      { bulkEdit(sellerOwner(r), w, r) }
func AdminBulkEdit(w http.ResponseWriter, r *http.Request)       { bulkEdit(adminOwner(r), w, r) }

// ── Importação ───────────────────────────────────────────────────────────────

func importPage(o catalogOwner, w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Base": o.Base}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, models.MaxImportBytes+64<<10)
		rows, err := readImportUpload(r)
		if err != nil {
			data["Erro"] = capitalizeFirst(err.Error()) + "."
		} else {
			id, err := models.CreateImportBatch(o.ID, rows)
			if err != nil {
				log.Printf("importação: %v", err)
				data["Erro"] = "Não foi possível guardar a planilha. Tente de novo."
			} else {
				go processImport(id, o.ID)
				http.Redirect(w, r, o.Base+"/importar?lote="+id, http.StatusSeeOther)
				return
			}
		}
	}
	if id := r.URL.Query().Get("lote"); id != "" {
		b, err := models.GetImportBatch(id, o.ID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		data["Lote"] = b
	}
	cats, _ := models.ListCategoriesFull()
	data["Categorias"] = cats
	o.render(w, r, o.page+"import.html", data)
}

func readImportUpload(r *http.Request) ([]models.ImportRow, error) {
	if err := r.ParseMultipartForm(models.MaxImportBytes); err != nil {
		return nil, fmt.Errorf("a planilha passa de %d MB; divida em arquivos menores", models.MaxImportBytes>>20)
	}
	file, _, err := r.FormFile("planilha")
	if err != nil {
		return nil, errors.New("escolha o arquivo da planilha (CSV)")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, models.MaxImportBytes+1))
	if err != nil {
		return nil, err
	}
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return nil, errors.New("isso é um arquivo do Excel (.xlsx); salve como CSV: Arquivo → Salvar como → CSV")
	}
	return models.ParseImportCSV(data)
}

// processImport consulta os ISBNs que faltam dados e valida a planilha. Roda
// em segundo plano; a página da prévia acompanha o progresso.
func processImport(id string, ownerID int) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("importação %s: %v", id, p)
		}
	}()
	b, err := models.GetImportBatch(id, ownerID)
	if err != nil {
		log.Printf("importação %s: %v", id, err)
		return
	}
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		done int
		sem  = make(chan struct{}, 4)
	)
	step := func() {
		done++
		if done%5 == 0 {
			models.SetImportProgress(id, done, len(b.Rows))
		}
	}
	for i := range b.Rows {
		if !b.Rows[i].NeedsLookup() {
			mu.Lock()
			step()
			mu.Unlock()
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(row *models.ImportRow) {
			defer wg.Done()
			defer func() { <-sem }()
			meta, found, err := services.LookupISBN(row.ISBN)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				row.Notes = append(row.Notes, "Não foi possível consultar o ISBN agora.")
			case !found:
				row.Notes = append(row.Notes, "ISBN não encontrado nas bases de livros.")
			default:
				row.ApplyMetadata(meta)
			}
			step()
		}(&b.Rows[i])
	}
	wg.Wait()
	models.ValidateImportRows(b.Rows, ownerID)
	b.Status, b.Done, b.Total = models.ImportReady, len(b.Rows), len(b.Rows)
	if err := models.SaveImportBatch(b); err != nil {
		log.Printf("importação %s: %v", id, err)
	}
}

func importPublish(o catalogOwner, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	id := r.FormValue("lote")
	ok, err := models.StartImportPublish(id, o.ID)
	if err != nil {
		log.Printf("importação %s: %v", id, err)
	}
	if ok {
		go publishImport(id, o.ID)
	}
	http.Redirect(w, r, o.Base+"/importar?lote="+id, http.StatusSeeOther)
}

// publishImport baixa as capas e cadastra os livros da prévia.
func publishImport(id string, ownerID int) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("publicação da importação %s: %v", id, p)
		}
	}()
	b, err := models.GetImportBatch(id, ownerID)
	if err != nil {
		log.Printf("publicação da importação %s: %v", id, err)
		return
	}
	// Progresso: cada capa a baixar e cada livro a gravar contam um passo.
	units, done := 0, 0
	for _, row := range b.Rows {
		if row.Ready() && row.Status == "" {
			units++
			if row.UpdateID == 0 && row.CoverURL != "" {
				units++
			}
		}
	}
	units = max(units, 1)
	// Capas primeiro, em paralelo: é a parte demorada.
	covers := make([]string, len(b.Rows))
	coverErr := make([]string, len(b.Rows))
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		sem = make(chan struct{}, 4)
	)
	for i := range b.Rows {
		row := b.Rows[i]
		if !row.Ready() || row.UpdateID > 0 || row.Status != "" || row.CoverURL == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, url string) {
			defer wg.Done()
			defer func() { <-sem }()
			u, err := saveRemoteImage(url)
			mu.Lock()
			if err != nil {
				coverErr[i] = err.Error()
			} else {
				covers[i] = u
			}
			done++
			if done%5 == 0 {
				models.SetImportProgress(id, done, units)
			}
			mu.Unlock()
		}(i, row.CoverURL)
	}
	wg.Wait()

	for i := range b.Rows {
		row := &b.Rows[i]
		if !row.Ready() || row.Status != "" {
			continue
		}
		done++
		if done%10 == 0 {
			models.SetImportProgress(id, done, units)
		}
		if row.UpdateID > 0 {
			if err := models.UpdatePriceStock(row.UpdateID, ownerID, row.Price, row.Stock); err != nil {
				row.Status, row.Result = "falhou", "Não foi possível atualizar o livro."
			} else {
				row.Status, row.Result = "atualizado", "Preço e estoque atualizados."
			}
			continue
		}
		img := covers[i]
		if img == "" {
			img = placeholderImage
		}
		if err := models.CreateProduct(row.Product(ownerID, img)); err != nil {
			if img != placeholderImage {
				removeUploadedImage(img)
			}
			msg := err.Error()
			if _, d, ok := strings.Cut(msg, ": "); ok {
				msg = d
			}
			row.Status, row.Result = "falhou", capitalizeFirst(msg)+"."
			continue
		}
		row.Status, row.Result = "publicado", "Publicado."
		if coverErr[i] != "" {
			row.Result = "Publicado com a capa gerada: " + coverErr[i] + "."
		}
	}
	b.Status, b.Done, b.Total = models.ImportDone, len(b.Rows), len(b.Rows)
	if err := models.SaveImportBatch(b); err != nil {
		log.Printf("publicação da importação %s: %v", id, err)
	}
}

// saveRemoteImage baixa a capa e grava em static/img/uploads.
func saveRemoteImage(url string) (string, error) {
	data, ct, err := services.FetchImage(url, maxImageBytes)
	if err != nil {
		return "", err
	}
	ext, ok := contentTypeExtension[ct]
	if !ok {
		return "", ErrInvalidImage
	}
	dir := filepath.Join("static", "img", "uploads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name, err := randomFileName(ext)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return "", err
	}
	return "/static/img/uploads/" + name, nil
}

// ImportTemplate - GET .../importar/modelo.csv: planilha modelo para o Excel.
func ImportTemplate(w http.ResponseWriter, r *http.Request) {
	rows := [][]string{
		models.ImportColumns,
		{"", "9788535914849", "", "", "", "", "Romance", "novo", "49,90", "3", "", "", "", "", "", ""},
		{"", "", "Dom Casmurro", "Machado de Assis", "Ática", "Português", "Clássicos", "usado", "18,00", "1",
			"Edição de 1997, capa com marcas de uso.", "https://exemplo.com/capa.jpg", "350", "21", "14", "2"},
	}
	writeCSV(w, "modelo-livredo.csv", rows)
}

func writeCSV(w http.ResponseWriter, name string, rows [][]string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	// BOM e ";" para o Excel em português abrir com acentos e colunas certas.
	w.Write([]byte("\xef\xbb\xbf"))
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	cw.WriteAll(rows)
}

// catalogExport - GET .../produtos/exportar.csv: o catálogo atual, no formato
// da importação. Com a coluna id, a planilha reenviada atualiza preço e
// estoque dos mesmos livros.
func catalogExport(o catalogOwner, w http.ResponseWriter, r *http.Request) {
	products, err := models.ListProductsBySeller(o.ID)
	if err != nil {
		http.Error(w, "erro ao exportar", http.StatusInternalServerError)
		return
	}
	rows := [][]string{models.ImportColumns}
	for _, p := range products {
		if p.IsDigital() {
			continue
		}
		cover := ""
		if strings.HasPrefix(p.ImageURL, "/static/img/uploads/") {
			cover = strings.TrimRight(models.GetSetting("store_base_url"), "/") + p.ImageURL
		}
		rows = append(rows, []string{
			strconv.Itoa(p.ID), p.ISBN, p.Name, p.Author, p.Publisher, p.Language, p.Category, p.Condition,
			p.PriceInput(), strconv.Itoa(p.Stock), p.Description, cover,
			strconv.Itoa(p.WeightG), strconv.Itoa(p.HeightCm), strconv.Itoa(p.WidthCm), strconv.Itoa(p.LengthCm),
		})
	}
	writeCSV(w, "catalogo-livredo.csv", rows)
}

// ── Edição em massa ──────────────────────────────────────────────────────────

func bulkEdit(o catalogOwner, w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Base": o.Base}
	if r.Method == http.MethodPost {
		r.ParseForm()
		changes, msg := bulkChangesFromForm(r)
		if msg == "" {
			n, err := models.BulkUpdateProducts(o.ID, changes)
			if err != nil {
				msg = capitalizeFirst(strings.TrimPrefix(err.Error(), models.ErrInvalidProduct.Error()+": ")) + "."
			} else {
				http.Redirect(w, r, fmt.Sprintf("%s/produtos/massa?salvos=%d", o.Base, n), http.StatusSeeOther)
				return
			}
		}
		data["Erro"] = msg
	}
	products, err := models.ListProductsBySeller(o.ID)
	if err != nil {
		log.Printf("edição em massa: %v", err)
	}
	data["Products"] = products
	if n := r.URL.Query().Get("salvos"); n != "" {
		data["Salvos"] = n
	}
	o.render(w, r, o.page+"bulk.html", data)
}

// bulkChangesFromForm lê a tabela e aplica a ação em massa aos marcados.
func bulkChangesFromForm(r *http.Request) ([]models.BulkChange, string) {
	selected := map[string]bool{}
	for _, id := range r.Form["sel"] {
		selected[id] = true
	}
	action := r.FormValue("acao")
	if r.FormValue("botao") != "aplicar" {
		action = ""
	}
	if action != "" && len(selected) == 0 {
		return nil, "Marque os livros que a ação deve alterar."
	}
	var value float64
	if action == "ajuste_preco" || action == "definir_estoque" || action == "somar_estoque" {
		v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(r.FormValue("valor")), ",", "."), 64)
		if err != nil {
			return nil, "Informe o valor da ação (por exemplo, -10 para baixar 10% o preço)."
		}
		value = v
	}
	var changes []models.BulkChange
	for _, idText := range r.Form["ids"] {
		id, err := strconv.Atoi(idText)
		if err != nil {
			continue
		}
		price, err := models.ParsePriceBR(r.FormValue("preco_" + idText))
		if err != nil {
			return nil, fmt.Sprintf("Preço inválido no livro %d: use, por exemplo, 39,90.", id)
		}
		stock, err := strconv.Atoi(strings.TrimSpace(r.FormValue("estoque_" + idText)))
		if err != nil && r.FormValue("estoque_"+idText) != "" {
			return nil, fmt.Sprintf("Estoque inválido no livro %d.", id)
		}
		c := models.BulkChange{ID: id, Price: price, Stock: stock, Active: r.FormValue("ativo_"+idText) == "on"}
		if selected[idText] {
			switch action {
			case "ajuste_preco":
				if value <= -100 || value > 1000 {
					return nil, "O ajuste de preço precisa ficar entre -99% e +1000%."
				}
				c.Price = c.Price * (1 + value/100)
			case "definir_estoque":
				c.Stock = int(value)
			case "somar_estoque":
				c.Stock += int(value)
				if c.Stock < 0 {
					c.Stock = 0
				}
			case "ativar":
				c.Active = true
			case "pausar":
				c.Active = false
			}
		}
		changes = append(changes, c)
	}
	return changes, ""
}

// ── Busca por ISBN no formulário ─────────────────────────────────────────────

// ISBNLookup - GET .../isbn?codigo=...: dados do livro em JSON para o
// formulário preencher os campos vazios.
func ISBNLookup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	isbn, ok := models.NormalizeISBN(r.URL.Query().Get("codigo"))
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"erro": "ISBN inválido: confira os dígitos."})
		return
	}
	meta, found, err := services.LookupISBN(isbn)
	switch {
	case err != nil:
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"erro": "Não foi possível consultar agora. Tente de novo."})
	case !found:
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"erro": "ISBN não encontrado nas bases de livros. Preencha à mão."})
	default:
		json.NewEncoder(w).Encode(map[string]any{"isbn": isbn, "dados": meta})
	}
}

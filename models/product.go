package models

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/vendas/loja/database"
)

// ErrInvalidProduct sinaliza dados de produto recusados na validação.
var ErrInvalidProduct = errors.New("produto inválido")

type Product struct {
	ID       int
	SellerID int
	Name     string
	// A descrição fica fora do JSON: o produto é guardado no cookie da sacola,
	// que tem limite de 4 KB, e duas sinopses longas estouravam esse limite.
	Description string `json:"-"`
	Price       float64
	Stock       int
	ImageURL    string
	Category    string
	Condition   string // ConditionNew ou ConditionUsed; vazio em e-books e em produtos anteriores ao campo
	Active      bool

	Format string // FormatPhysical ou FormatDigital; escolhido no cadastro e fixo depois
	Author string
	// Ficha bibliográfica: ISBN-13 só com dígitos, editora e idioma.
	ISBN      string
	Publisher string
	Language  string
	// Arquivo do e-book: nome gerado, guardado em DataDir()/ebooks. Nunca vai
	// para o cookie da sacola nem para as páginas.
	EbookFile string `json:"-"`
	EbookType string // EbookPDF ou EbookEPUB
	EbookSize int64
	// EbookSHA256 identifica o arquivo: o mesmo e-book não pode estar à venda
	// duas vezes (barra o PDF pirata repostado por vários vendedores).
	EbookSHA256 string `json:"-"`
	// EbookPages é o número de páginas do arquivo (0 = não contado, sem
	// amostra). PreviewPages é a escolha do vendedor para a amostra de
	// leitura ("1-4, 10"); vazio usa as primeiras páginas.
	EbookPages   int
	PreviewPages string
	// Audiobook (audio.go): preços das edições e o resumo das faixas.
	AudioPrice   float64
	BundlePrice  float64
	AudioTracks  int
	AudioSeconds int
	// FastShipper: o vendedor tem a medalha Envio Rápido (dispatch.go).
	FastShipper bool `json:"-"`
	// RightsDeclared: o formulário confirmou agora que quem envia tem os
	// direitos da obra. Gravado como data em rights_declared_at.
	RightsDeclared bool `json:"-"`

	// Peso (g) e medidas (cm) do livro embalado, para o frete. Zero usa o
	// pacote padrão de livro (shipping.go).
	WeightG  int
	HeightCm int
	WidthCm  int
	LengthCm int

	// Quem vende, preenchido na vitrine e na página do produto. SellerID 0 é
	// produto cadastrado pelo admin: vendido pelo próprio Livredo.
	SellerName string
	// SellerType é "Loja" ou "Pessoa física", calculado no banco a partir do
	// CPF/CNPJ. O documento em si nunca é carregado aqui: o produto vai para o
	// cookie da sacola do comprador, e o CPF do vendedor não pode ir junto.
	SellerType string
	// SellerReady: o vendedor tem conta Mercado Pago conectada e pode receber.
	// Livro do próprio site está sempre pronto.
	SellerReady bool
}

// SoldBySite indica produto do próprio Livredo, cadastrado pelo admin.
func (p Product) SoldBySite() bool {
	return p.SellerID == 0
}

// SellerLabel é o nome exibido em "vendido por".
func (p Product) SellerLabel() string {
	if p.SoldBySite() || p.SellerName == "" {
		return "Livredo"
	}
	return p.SellerName
}

// SellerKind devolve "Loja" (CNPJ) ou "Pessoa física" (CPF), derivado do
// documento que o vendedor informou no cadastro. Vazio para o próprio site e
// para vendedores antigos, sem documento.
func (p Product) SellerKind() string {
	if p.SoldBySite() {
		return ""
	}
	return p.SellerType
}

// Estado do exemplar. É independente do gênero: um mangá pode ser novo ou
// usado, então isso não cabe como categoria.
const (
	ConditionNew  = "novo"
	ConditionUsed = "usado"
)

func (p Product) FormattedPrice() string {
	return FormatBRL(p.Price)
}

// ConditionLabel devolve "Novo", "Usado" ou vazio quando o vendedor ainda não
// informou — produtos cadastrados antes do campo existir não recebem um estado
// presumido, para a loja não anunciar como novo um livro que pode ser usado.
func (p Product) ConditionLabel() string {
	switch p.Condition {
	case ConditionNew:
		return "Novo"
	case ConditionUsed:
		return "Usado"
	default:
		return ""
	}
}

// ValidCondition informa se o valor é um estado aceito para um livro físico.
func ValidCondition(condition string) bool {
	return condition == ConditionNew || condition == ConditionUsed
}

// ValidShelfFilter informa se o valor serve de filtro da vitrine: novos,
// usados ou digitais. Para quem compra são três prateleiras, embora "digital"
// seja um formato e não um estado.
func ValidShelfFilter(filter string) bool {
	return ValidCondition(filter) || filter == FormatDigital
}

// productColumns e productScanArgs mantêm todas as consultas de produto com
// as mesmas colunas, na mesma ordem.
const productColumns = `p.id, p.seller_id, p.name, p.description, p.price, p.stock, p.image_url, p.category, p.item_condition,
	p.product_format, p.author, p.ebook_file, p.ebook_type, p.ebook_size,
	p.weight_g, p.height_cm, p.width_cm, p.length_cm, p.ebook_sha256, p.ebook_pages, p.preview_pages, p.isbn, p.publisher, p.language,
	p.audio_price, p.bundle_price,
	(SELECT COUNT(*) FROM audio_tracks at WHERE at.product_id = p.id),
	(SELECT COALESCE(SUM(at.duration_sec), 0) FROM audio_tracks at WHERE at.product_id = p.id)`

func productScanArgs(p *Product) []any {
	return []any{&p.ID, &p.SellerID, &p.Name, &p.Description, &p.Price, &p.Stock, &p.ImageURL, &p.Category, &p.Condition,
		&p.Format, &p.Author, &p.EbookFile, &p.EbookType, &p.EbookSize,
		&p.WeightG, &p.HeightCm, &p.WidthCm, &p.LengthCm, &p.EbookSHA256, &p.EbookPages, &p.PreviewPages, &p.ISBN, &p.Publisher, &p.Language,
		&p.AudioPrice, &p.BundlePrice, &p.AudioTracks, &p.AudioSeconds}
}

// ListProducts lista a vitrine, com filtro opcional por gênero e por
// prateleira (novos, usados ou digitais). Os dois filtros combinam: "mangás
// usados", "romances digitais".
func ListProducts(category, shelf string) ([]Product, error) {
	query := `SELECT ` + productColumns + `,
		COALESCE(s.name, ''),
		CASE LENGTH(COALESCE(s.document, '')) WHEN 14 THEN 'Loja' WHEN 11 THEN 'Pessoa física' ELSE '' END
		FROM products p LEFT JOIN sellers s ON s.id = p.seller_id
		WHERE p.active=1
		  -- Só entram livros de quem pode receber: sem conta Mercado Pago
		  -- conectada, não haveria para onde mandar o pagamento.
		  AND (p.seller_id = 0 OR (s.active = 1 AND s.mp_access_token <> '' AND s.listing_paused = 0))
		  -- E-book só de vendedor aprovado como autor ou editora (pirataria).
		  AND (p.product_format <> 'digital' OR p.seller_id = 0 OR s.digital_status = 'aprovado')`
	args := []any{}
	if category != "" && category != "Todos" {
		query += ` AND p.category=?`
		args = append(args, category)
	}
	switch {
	case shelf == FormatDigital:
		query += ` AND p.product_format='digital'`
	case ValidCondition(shelf):
		query += ` AND p.product_format='fisico' AND p.item_condition=?`
		args = append(args, shelf)
	}
	query += ` ORDER BY p.id`
	rows, err := database.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var products []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(append(productScanArgs(&p), &p.SellerName, &p.SellerType)...); err != nil {
			return nil, err
		}
		p.Active = true
		p.SellerReady = true
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	SortByRelevance(products)
	return products, nil
}

func GetProduct(id int) (*Product, error) {
	row := database.DB.QueryRow(`SELECT `+productColumns+`, p.active,
		COALESCE(s.name, ''),
		CASE LENGTH(COALESCE(s.document, '')) WHEN 14 THEN 'Loja' WHEN 11 THEN 'Pessoa física' ELSE '' END,
		CASE WHEN p.seller_id = 0 OR (s.active = 1 AND s.mp_access_token <> ''
			AND (p.product_format <> 'digital' OR s.digital_status = 'aprovado')) THEN 1 ELSE 0 END
		FROM products p LEFT JOIN sellers s ON s.id = p.seller_id
		WHERE p.id=? AND p.archived=0`, id)
	var p Product
	var active int
	err := row.Scan(append(productScanArgs(&p), &active, &p.SellerName, &p.SellerType, &p.SellerReady)...)
	if err != nil {
		return nil, err
	}
	p.Active = active == 1
	return &p, nil
}

// ListCategories monta o filtro da vitrine a partir das categorias que o
// administrador cadastrou e que realmente têm produtos ativos.
func ListCategories() ([]string, error) {
	rows, err := database.DB.Query(`
		SELECT c.name FROM categories c
		WHERE EXISTS (SELECT 1 FROM products p WHERE p.category = c.name AND p.active = 1)
		ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cats := []string{"Todos"}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cats = append(cats, c)
	}
	return cats, rows.Err()
}

// validateProduct impede que valores absurdos ou uma categoria inventada entrem
// no catálogo, venham eles do painel do vendedor ou de um POST forjado.
func validateProduct(p *Product) error {
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.Category = strings.TrimSpace(p.Category)
	p.Author = strings.TrimSpace(p.Author)
	p.Publisher = strings.Join(strings.Fields(p.Publisher), " ")
	p.Language = strings.Join(strings.Fields(p.Language), " ")
	if strings.TrimSpace(p.ISBN) != "" {
		isbn, ok := NormalizeISBN(p.ISBN)
		if !ok {
			return fmt.Errorf("%w: ISBN inválido (confira os dígitos)", ErrInvalidProduct)
		}
		p.ISBN = isbn
	} else {
		p.ISBN = ""
	}
	if len([]rune(p.Publisher)) > 120 || len([]rune(p.Language)) > 40 {
		return fmt.Errorf("%w: editora ou idioma muito longo", ErrInvalidProduct)
	}

	if p.Name == "" || len([]rune(p.Name)) > 120 {
		return fmt.Errorf("%w: informe um nome de até 120 caracteres", ErrInvalidProduct)
	}
	if len([]rune(p.Description)) > 2000 {
		return fmt.Errorf("%w: descrição muito longa", ErrInvalidProduct)
	}
	if len([]rune(p.Author)) > 120 {
		return fmt.Errorf("%w: nome do autor muito longo", ErrInvalidProduct)
	}
	if p.Price <= 0 || p.Price > 1_000_000 || math.IsNaN(p.Price) || math.IsInf(p.Price, 0) {
		return fmt.Errorf("%w: preço deve estar entre R$ 0,01 e R$ 1.000.000", ErrInvalidProduct)
	}
	p.Price = math.Round(p.Price*100) / 100
	if !CategoryExists(p.Category) {
		return fmt.Errorf("%w: selecione uma categoria cadastrada", ErrInvalidProduct)
	}
	switch p.Format {
	case "", FormatPhysical:
		p.Format = FormatPhysical
		p.EbookFile, p.EbookType, p.EbookSize = "", "", 0
		if p.Stock < 0 || p.Stock > 1_000_000 {
			return fmt.Errorf("%w: estoque fora do intervalo permitido", ErrInvalidProduct)
		}
		if !ValidCondition(p.Condition) {
			return fmt.Errorf("%w: informe se o livro é novo ou usado", ErrInvalidProduct)
		}
		if err := validatePackage(p); err != nil {
			return err
		}
	case FormatDigital:
		// E-book não tem estado nem estoque: não se gasta ao ser vendido.
		p.Condition = ""
		p.Stock = 0
		p.WeightG, p.HeightCm, p.WidthCm, p.LengthCm = 0, 0, 0, 0
		if !ValidEbookFileName(p.EbookFile) || !ValidEbookType(p.EbookType) {
			return fmt.Errorf("%w: envie o arquivo do e-book (PDF, EPUB, CBR ou CBZ)", ErrInvalidProduct)
		}
	default:
		return fmt.Errorf("%w: formato inválido", ErrInvalidProduct)
	}
	if !validImageURL(p.ImageURL) {
		return fmt.Errorf("%w: imagem inválida", ErrInvalidProduct)
	}
	return nil
}

// validImageURL restringe a imagem a arquivos servidos pela própria loja.
// Aceitar uma URL externa arbitrária permitiria apontar para conteúdo de
// terceiros e usar a vitrine para rastrear quem a visita.
func validImageURL(url string) bool {
	if url == "" {
		return false
	}
	if strings.Contains(url, "..") || strings.Contains(url, "\\") {
		return false
	}
	return strings.HasPrefix(url, "/static/img/")
}

func CreateProduct(p Product) error {
	if err := validateProduct(&p); err != nil {
		return err
	}
	if err := checkDigitalRights(&p); err != nil {
		return err
	}
	_, err := database.DB.Exec(`INSERT INTO products (seller_id, name, description, price, stock, image_url, category, item_condition,
		product_format, author, ebook_file, ebook_type, ebook_size, weight_g, height_cm, width_cm, length_cm, ebook_sha256, ebook_pages, preview_pages, isbn, publisher, language, rights_declared_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?, CASE WHEN ? THEN CURRENT_TIMESTAMP END)`,
		p.SellerID, p.Name, p.Description, p.Price, p.Stock, p.ImageURL, p.Category, p.Condition,
		p.Format, p.Author, p.EbookFile, p.EbookType, p.EbookSize, p.WeightG, p.HeightCm, p.WidthCm, p.LengthCm, p.EbookSHA256, p.EbookPages, p.PreviewPages, p.ISBN, p.Publisher, p.Language, p.RightsDeclared)
	return err
}

func UpdateProduct(p Product) error {
	if err := validateProduct(&p); err != nil {
		return err
	}
	if err := checkDigitalRights(&p); err != nil {
		return err
	}
	// O formato não muda depois do cadastro (product_format no WHERE): um
	// e-book já vendido não pode virar livro físico e tirar o download de quem
	// comprou.
	result, err := database.DB.Exec(`UPDATE products SET seller_id=?, name=?, description=?, price=?, stock=?, image_url=?, category=?, item_condition=?, active=?,
		author=?, ebook_file=?, ebook_type=?, ebook_size=?, weight_g=?, height_cm=?, width_cm=?, length_cm=?, ebook_sha256=?, ebook_pages=?, preview_pages=?, isbn=?, publisher=?, language=?,
		rights_declared_at = CASE WHEN ? THEN CURRENT_TIMESTAMP ELSE rights_declared_at END
		WHERE id=? AND product_format=?`,
		p.SellerID, p.Name, p.Description, p.Price, p.Stock, p.ImageURL, p.Category, p.Condition, boolToInt(p.Active),
		p.Author, p.EbookFile, p.EbookType, p.EbookSize, p.WeightG, p.HeightCm, p.WidthCm, p.LengthCm, p.EbookSHA256, p.EbookPages, p.PreviewPages, p.ISBN, p.Publisher, p.Language, p.RightsDeclared, p.ID, p.Format)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: o formato não pode ser trocado depois do cadastro", ErrInvalidProduct)
	}
	return nil
}

// UpdateProductBySeller impede que um vendedor altere o produto de outro, mesmo
// forjando o campo id do formulário.
func UpdateProductBySeller(p Product, sellerID int) error {
	if err := validateProduct(&p); err != nil {
		return err
	}
	p.SellerID = sellerID
	if err := checkDigitalRights(&p); err != nil {
		return err
	}
	result, err := database.DB.Exec(
		`UPDATE products SET name=?, description=?, price=?, stock=?, image_url=?, category=?, item_condition=?, active=?,
		author=?, ebook_file=?, ebook_type=?, ebook_size=?, weight_g=?, height_cm=?, width_cm=?, length_cm=?, ebook_sha256=?, ebook_pages=?, preview_pages=?, isbn=?, publisher=?, language=?,
		rights_declared_at = CASE WHEN ? THEN CURRENT_TIMESTAMP ELSE rights_declared_at END
		WHERE id=? AND seller_id=? AND product_format=?`,
		p.Name, p.Description, p.Price, p.Stock, p.ImageURL, p.Category, p.Condition, boolToInt(p.Active),
		p.Author, p.EbookFile, p.EbookType, p.EbookSize, p.WeightG, p.HeightCm, p.WidthCm, p.LengthCm, p.EbookSHA256, p.EbookPages, p.PreviewPages, p.ISBN, p.Publisher, p.Language, p.RightsDeclared, p.ID, sellerID, p.Format,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrInvalidProduct
	}
	return nil
}

// DeleteProduct exclui o produto. Se ele nunca foi vendido, a linha é apagada
// de verdade e removed volta true, junto com a imagem, para quem chamou poder
// apagar o arquivo. Se já aparece em algum pedido, apagar quebraria o
// histórico desse pedido; então ele é arquivado: some da loja, dos painéis e
// da contagem da categoria, mas continua existindo para os pedidos antigos.
func DeleteProduct(id int) (removed bool, imageURL string, err error) {
	tx, err := database.DB.Begin()
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback()

	if err := tx.QueryRow(`SELECT image_url FROM products WHERE id=? AND archived=0`, id).Scan(&imageURL); err != nil {
		return false, "", err
	}
	var vendas int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM order_items WHERE product_id=?`, id).Scan(&vendas); err != nil {
		return false, "", err
	}
	if vendas > 0 {
		_, err = tx.Exec(`UPDATE products SET active=0, archived=1 WHERE id=?`, id)
		imageURL = ""
	} else {
		_, err = tx.Exec(`DELETE FROM products WHERE id=?`, id)
		removed = true
	}
	if err != nil {
		return false, "", err
	}
	if err := tx.Commit(); err != nil {
		return false, "", err
	}
	return removed, imageURL, nil
}

// ImageInUse informa se outro produto ainda usa a imagem, antes de apagar o
// arquivo do disco.
func ImageInUse(imageURL string) bool {
	var n int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM products WHERE image_url=?`, imageURL).Scan(&n); err != nil {
		return true
	}
	return n > 0
}

func ListAllProducts() ([]Product, error) {
	rows, err := database.DB.Query(`SELECT ` + productColumns + `, p.active FROM products p WHERE p.archived=0 ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var products []Product
	for rows.Next() {
		var p Product
		var active int
		if err := rows.Scan(append(productScanArgs(&p), &active)...); err != nil {
			return nil, err
		}
		p.Active = active == 1
		products = append(products, p)
	}
	return products, rows.Err()
}

func ListProductsBySeller(sellerID int) ([]Product, error) {
	rows, err := database.DB.Query(`SELECT `+productColumns+`, p.active FROM products p WHERE p.seller_id=? AND p.archived=0 ORDER BY p.id`, sellerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var products []Product
	for rows.Next() {
		var p Product
		var active int
		if err := rows.Scan(append(productScanArgs(&p), &active)...); err != nil {
			return nil, err
		}
		p.Active = active == 1
		products = append(products, p)
	}
	return products, rows.Err()
}

// SetProductActive tira um livro da loja (ou o devolve) sem apagá-lo, usado
// pelo admin ao atender uma denúncia.
func SetProductActive(id int, active bool) error {
	_, err := database.DB.Exec(`UPDATE products SET active=? WHERE id=?`, boolToInt(active), id)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

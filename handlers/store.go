package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vendas/loja/models"
	"github.com/vendas/loja/services"
)

const (
	// pixExpiration mantém o QR Code válido por tempo suficiente para o cliente
	// abrir o app do banco, sem prender o estoque do pedido indefinidamente.
	pixExpiration = 30 * time.Minute
	// checkoutExpiration vale para cartão e boleto, que levam mais tempo.
	checkoutExpiration = 24 * time.Hour
	// maxInstallments limita o parcelamento oferecido no Checkout Pro.
	maxInstallments = 12
)

// pages armazena um template set por página, cada um parseado apenas com seu layout.
var pages map[string]*template.Template

// templateFuncs fica disponível em todas as páginas. brl escreve valores no
// formato brasileiro (R$ 1.234,56); capaTom escolhe a cor da capa tipográfica
// dos livros sem foto.
var templateFuncs = template.FuncMap{
	"brl":     models.FormatBRL,
	"sub":     func(a, b float64) float64 { return a - b },
	"add":     func(a, b float64) float64 { return a + b },
	"capaTom": coverTone,
	"dict":    dict,
	"inicial": initial,
	"list":    func(v ...string) []string { return v },
	"comeca":  strings.HasPrefix,
	"pct":     models.PercentLabel,
	"data":    formatarData,
	// notas/notasDesc: as cinco estrelas da avaliação, em ordem e invertidas.
	"notas":     func() []int { return []int{1, 2, 3, 4, 5} },
	"notasDesc": func() []int { return []int{5, 4, 3, 2, 1} },
}

// coverTones são as cores das capas geradas, todas da paleta da marca.
var coverTones = []string{"capa-musgo", "capa-canela", "capa-papel", "capa-cafe", "capa-caramelo", "capa-camurca"}

// coverTone deriva a cor do ID do produto: o mesmo livro sempre tem a mesma
// capa, e livros vizinhos na estante ganham cores diferentes.
func coverTone(id int) string {
	if id < 0 {
		id = -id
	}
	return coverTones[id%len(coverTones)]
}

// parsePage monta o conjunto de templates de uma página. O nome do conjunto
// é o do primeiro arquivo: para as páginas avulsas (logins e cadastro de
// vendedor), que são executadas com Execute, a própria página vem primeiro.
func parsePage(files ...string) *template.Template {
	return template.Must(template.New(filepath.Base(files[0])).Funcs(templateFuncs).ParseFiles(files...))
}

// Init pré-compila cada página junto com seu respectivo layout.
// Isso evita o conflito de nomes ({{define "content"}}) que ocorre
// quando todos os templates são parseados num único conjunto.
func Init() {
	os.MkdirAll(filepath.Join("static", "img", "uploads"), 0755)

	pages = make(map[string]*template.Template)

	// head.html traz fontes, Tailwind, paleta e favicon da marca, e entra em
	// todos os conjuntos para que loja, admin e vendedor compartilhem a mesma
	// identidade visual a partir de um único arquivo.
	head := filepath.Join("templates", "partials", "head.html")
	capa := filepath.Join("templates", "partials", "capa.html")
	// perfil.html: avatar, estrelas e selo de nível do vendedor.
	perfil := filepath.Join("templates", "partials", "perfil.html")
	// icones.html: ícones do menu lateral dos painéis.
	icones := filepath.Join("templates", "partials", "icones.html")
	// livro_form.html: campos de formato e e-book dos formulários de livro.
	livroForm := filepath.Join("templates", "partials", "livro_form.html")
	// autor_form.html: formulário da página do autor (vendedor e admin).
	autorForm := filepath.Join("templates", "partials", "autor_form.html")
	// catalogo.html: importação por planilha e edição em massa.
	catalogo := filepath.Join("templates", "partials", "catalogo.html")
	// audio.html: faixas e preços do audiobook.
	audio := filepath.Join("templates", "partials", "audio.html")
	// envio.html: endereço de despacho, conta Melhor Envio e etiqueta.
	envio := filepath.Join("templates", "partials", "envio.html")
	layout := filepath.Join("templates", "layout.html")
	adminLayout := filepath.Join("templates", "admin", "layout.html")
	sellerLayout := filepath.Join("templates", "seller", "layout.html")

	storePages := []string{
		"home.html", "product.html", "cart.html",
		"checkout.html", "order_confirm.html", "pix_payment.html",
		"payment_processing.html", "payment_redirect.html",
		"account_register.html", "account_login.html", "account.html", "account_orders.html", "account_library.html",
		"account_privacy.html", "terms.html", "privacy.html", "terms_accept.html", "account_listen.html",
		"report.html", "author.html",
	}
	for _, p := range storePages {
		pages[p] = parsePage(layout, head, capa, perfil, filepath.Join("templates", p))
	}

	adminPages := []string{
		"admin_dashboard.html", "admin_products.html", "admin_product_form.html",
		"admin_orders.html", "admin_order_detail.html", "admin_settings.html",
		"admin_sellers.html", "admin_categories.html", "admin_reports.html", "admin_shipping.html", "admin_digital.html",
		"admin_authors.html", "admin_import.html", "admin_bulk.html", "admin_audio.html",
	}
	for _, p := range adminPages {
		pages[p] = parsePage(adminLayout, head, perfil, icones, livroForm, autorForm, catalogo, audio, envio, filepath.Join("templates", p))
	}

	sellerPages := []string{"seller_dashboard.html", "seller_products.html", "seller_product_form.html", "seller_orders.html", "seller_order_detail.html", "seller_shipping.html", "seller_digital.html", "seller_authors.html", "seller_import.html", "seller_bulk.html", "seller_audio.html"}
	for _, p := range sellerPages {
		pages[p] = parsePage(sellerLayout, head, perfil, icones, livroForm, autorForm, catalogo, audio, envio, filepath.Join("templates", p))
	}

	for _, p := range []string{"admin_login.html", "seller_login.html", "seller_register.html"} {
		pages[p] = parsePage(filepath.Join("templates", p), head)
	}
}

// renderStore executa um template de loja usando o layout principal.
func renderStore(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	t, ok := pages[page]
	if !ok {
		http.Error(w, "template não encontrado: "+page, 500)
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	if _, ok := data["CartCount"]; !ok {
		data["CartCount"] = getCart(r).Count()
	}
	data["CSRFToken"] = csrfToken(r)
	data["Year"] = time.Now().Year()
	// O menu do cabeçalho destaca o atalho ativo comparando estes valores;
	// fora da vitrine eles não existem e precisam de um vazio explícito.
	for _, key := range []string{"SelectedCat", "Estado"} {
		if _, ok := data[key]; !ok {
			data[key] = ""
		}
	}
	data["NaVitrine"] = page == "home.html"
	if page == "account_login.html" || page == "account_register.html" {
		data["GoogleLogin"] = googleLoginAvailable()
		if aviso, ok := googleAvisos[r.URL.Query().Get("google")]; ok {
			data["GoogleAviso"] = aviso
		}
	}

	var customer *models.Customer
	if isCustomerLoggedIn(r) {
		if c, err := models.GetCustomerByID(currentCustomerID(r)); err == nil {
			customer = c
			data["CustomerName"] = c.Name
			data["CustomerAvatar"] = c.AvatarURL
		}
	}
	data["IsCustomerLoggedIn"] = customer != nil

	name, tierLabel, icon, pillClass, arrowColor := cursorDisplay(r, customer)
	data["CursorName"] = name
	data["CursorTierLabel"] = tierLabel
	data["CursorIcon"] = icon
	data["CursorPillClass"] = pillClass
	data["CursorArrowColor"] = arrowColor

	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("renderStore %s: %v", page, err)
	}
}

// loyaltyTiers define os níveis de fidelidade por valor total gasto em pedidos
// confirmados. Cores: Bronze (nível baixo), Ouro (nível médio), Esmeralda (nível alto).
var loyaltyTiers = []struct {
	MinSpent   float64
	Label      string
	Icon       string
	PillClass  string
	ArrowColor string
}{
	{1000, "Esmeralda", "💎", "bg-[#0f9d58] text-white", "#0f9d58"},
	{300, "Ouro", "🥇", "bg-[#d4af37] text-[#4a3300]", "#d4af37"},
	{0, "Bronze", "🥉", "bg-[#b08d57] text-white", "#b08d57"},
}

// cursorDisplay monta os dados do cursor customizado: admin logado mostra seu
// usuário, cliente logado mostra nome + nível de fidelidade, e visitantes
// anônimos veem o nome da loja — todos com o estilo padrão neutro.
func cursorDisplay(r *http.Request, customer *models.Customer) (name, tierLabel, icon, pillClass, arrowColor string) {
	const defaultPillClass = "bg-musgo-800 text-papel-50"
	const defaultArrowColor = "#C0963F" // ouro da marca

	if isAdminLoggedIn(r) {
		name = adminUsername(r)
		if name == "" {
			name = "Admin"
		}
		return name, "", "", defaultPillClass, defaultArrowColor
	}

	if customer != nil {
		spent, _ := models.TotalSpentByCustomer(customer.ID)
		for _, tier := range loyaltyTiers {
			if spent >= tier.MinSpent {
				return customer.Name, tier.Label, tier.Icon, tier.PillClass, tier.ArrowColor
			}
		}
	}

	storeName := models.GetSetting("store_name")
	if storeName == "" {
		storeName = "Livredo"
	}
	return storeName, "", "", defaultPillClass, defaultArrowColor
}

// renderAdmin executa um template de admin usando o layout admin.
func renderAdmin(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	t, ok := pages[page]
	if !ok {
		http.Error(w, "template não encontrado: "+page, 500)
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["CSRFToken"] = csrfToken(r)
	var err error
	if page == "admin_login.html" {
		err = t.Execute(w, data)
	} else {
		data["OpenReports"] = models.CountOpenReports(0)
		data["PendingDigital"] = models.CountPendingDigital()
		data["Path"] = r.URL.Path // destaca a página atual no menu
		err = t.ExecuteTemplate(w, "admin_layout", data)
	}
	if err != nil {
		log.Printf("renderAdmin %s: %v", page, err)
	}
}

func renderSeller(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	t, ok := pages[page]
	if !ok {
		http.Error(w, "template não encontrado: "+page, 500)
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["CSRFToken"] = csrfToken(r)
	var err error
	if page == "seller_login.html" || page == "seller_register.html" {
		data["GoogleLogin"] = googleLoginAvailable()
		if aviso, ok := sellerGoogleAvisos[r.URL.Query().Get("google")]; ok {
			data["GoogleAviso"] = aviso.texto
		}
		err = t.Execute(w, data)
	} else {
		data["Path"] = r.URL.Path
		err = t.ExecuteTemplate(w, "seller_layout", data)
	}
	if err != nil {
		log.Printf("renderSeller %s: %v", page, err)
	}
}

func getCart(r *http.Request) *models.Cart {
	sess, _ := store.Get(r, "session")
	raw, ok := sess.Values["cart"]
	if !ok {
		return &models.Cart{}
	}
	data, ok := raw.([]byte)
	if !ok {
		return &models.Cart{}
	}
	var cart models.Cart
	if err := json.Unmarshal(data, &cart); err != nil {
		return &models.Cart{}
	}
	return &cart
}

func saveCart(w http.ResponseWriter, r *http.Request, cart *models.Cart) {
	sess, _ := store.Get(r, "session")
	data, _ := json.Marshal(cart)
	sess.Values["cart"] = data
	sess.Save(r, w)
}

// Home - página inicial com lista de produtos
func Home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	category := r.URL.Query().Get("categoria")
	search := r.URL.Query().Get("q")
	// Prateleira fora de novo/usado/digital é ignorada, para um valor
	// qualquer na URL não esvaziar a vitrine.
	condition := r.URL.Query().Get("estado")
	if !models.ValidShelfFilter(condition) {
		condition = ""
	}

	products, err := models.ListProducts(category, condition)
	if err != nil {
		log.Printf("ListProducts: %v", err)
		products = []models.Product{}
	}

	if search != "" {
		var filtered []models.Product
		for _, p := range products {
			if containsCI(p.Name, search) || containsCI(p.Author, search) || containsCI(p.Description, search) {
				filtered = append(filtered, p)
			}
		}
		products = filtered
	}

	categories, _ := models.ListCategories()
	cart := getCart(r)

	renderStore(w, r, "home.html", map[string]any{
		"Products":    products,
		"Categories":  categories,
		"SelectedCat": category,
		"Estado":      condition,
		"Search":      search,
		"CartCount":   cart.Count(),
	})
}

// ProductDetail - página de detalhes do produto
func ProductDetail(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := models.GetProduct(id)
	if err != nil || !p.Active {
		http.NotFound(w, r)
		return
	}
	cart := getCart(r)
	data := map[string]any{
		"Product":   p,
		"CartCount": cart.Count(),
	}
	if p.IsDigital() {
		data["OwnsEbook"] = models.CustomerOwnsProduct(currentCustomerID(r), p.ID)
		data["OwnsAudio"] = models.CustomerOwnsAudio(currentCustomerID(r), p.ID)
		data["Edicoes"] = editionsToBuy(p, data["OwnsEbook"].(bool), data["OwnsAudio"].(bool))
		if p.HasAudio() {
			if tracks, err := models.ListAudioTracks(p.ID); err == nil {
				data["Faixas"] = tracks
				if url := audioSampleURL(p, tracks); url != "" {
					data["AmostraAudio"] = url
				}
			}
		}
		data["Direitos"] = models.GetDigitalRights(p.SellerID)
		if amostra := ebookPreviewData(p); amostra != nil {
			data["Amostra"] = amostra
		}
	}
	if !p.SoldBySite() {
		if seller, err := models.GetSeller(p.SellerID); err == nil {
			data["SellerAvatar"] = seller.AvatarURL
		}
		data["SellerRating"] = models.GetSellerRating(p.SellerID)
		if !p.IsDigital() {
			data["EnvioRapido"] = models.ShippingBadges([]int{p.SellerID})[p.SellerID].FastShipper
			data["PrazoPostagem"] = models.SLAHours(p.SellerID) / 24
		}
		// O próprio autor vendendo: o cartão do vendedor leva à página dele.
		if d := models.GetDigitalRights(p.SellerID); p.Author != "" && d.Kind == models.DigitalKindAuthor && d.AllowsAuthor(p.Author) {
			data["VendedorEhAutor"] = true
		}
	}
	if p.SellerReady && p.Available() {
		if plan := installmentPreview(p.SellerID, p.Price); plan != nil {
			data["Parcelas"] = plan
		}
	}
	renderStore(w, r, "product.html", data)
}

// CartView - exibe o carrinho
func CartView(w http.ResponseWriter, r *http.Request) {
	cart := getCart(r)
	data := map[string]any{
		"Cart":      cart,
		"CartCount": cart.Count(),
	}
	_, vendedor, temItens := cart.Seller()
	if temItens {
		data["Vendedor"] = vendedor
	}
	switch r.URL.Query().Get("aviso") {
	case "vendedor":
		data["Aviso"] = "Cada compra vai para um único vendedor. Finalize esta sacola (ou esvazie-a) antes de levar livros de outro vendedor."
	case "indisponivel":
		data["Aviso"] = "Esse vendedor ainda não está recebendo pagamentos, então o livro não pode ser comprado agora."
	case "estoque":
		data["Aviso"] = "Algum livro da sacola acabou de esgotar. Ajuste as quantidades e tente de novo."
	case "ebook-indisponivel":
		data["Aviso"] = "Um e-book da sacola não está mais à venda. Remova-o para continuar."
	case "ja-tem":
		data["Aviso"] = "Você já tem esse livro digital: ele está em Minha biblioteca, pronto para baixar."
	}
	renderStore(w, r, "cart.html", data)
}

// CartAdd - adiciona produto ao carrinho
func CartAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	r.ParseForm()
	id, _ := strconv.Atoi(r.FormValue("id"))
	qty, _ := strconv.Atoi(r.FormValue("qty"))
	if qty <= 0 {
		qty = 1
	}
	p, err := models.GetProduct(id)
	if err != nil || !p.Active || !p.Available() {
		http.Error(w, "produto não encontrado", 400)
		return
	}
	if !p.SellerReady {
		http.Redirect(w, r, "/carrinho?aviso=indisponivel", http.StatusSeeOther)
		return
	}
	edition, ok := p.ValidEdition(r.FormValue("edicao"))
	if !ok {
		http.Redirect(w, r, fmt.Sprintf("/produto?id=%d", p.ID), http.StatusSeeOther)
		return
	}
	if p.IsDigital() {
		if models.CustomerOwnsEdition(currentCustomerID(r), p.ID, edition) {
			http.Redirect(w, r, "/carrinho?aviso=ja-tem", http.StatusSeeOther)
			return
		}
	} else if qty > p.Stock {
		qty = p.Stock
	}
	cart := getCart(r)
	// Um pagamento vai para um único recebedor: a sacola aceita livros de um
	// vendedor por vez. O pedido confere de novo no servidor.
	if sellerID, _, ok := cart.Seller(); ok && sellerID != p.SellerID {
		http.Redirect(w, r, "/carrinho?aviso=vendedor", http.StatusSeeOther)
		return
	}
	cart.Add(*p, qty, edition)
	saveCart(w, r, cart)
	http.Redirect(w, r, "/carrinho", http.StatusSeeOther)
}

// CartUpdate - atualiza quantidade no carrinho
func CartUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	r.ParseForm()
	id, _ := strconv.Atoi(r.FormValue("id"))
	qty, _ := strconv.Atoi(r.FormValue("qty"))
	cart := getCart(r)
	cart.UpdateQty(id, qty)
	saveCart(w, r, cart)
	http.Redirect(w, r, "/carrinho", http.StatusSeeOther)
}

// CartRemove - remove item do carrinho
func CartRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	r.ParseForm()
	id, _ := strconv.Atoi(r.FormValue("id"))
	cart := getCart(r)
	cart.Remove(id)
	saveCart(w, r, cart)
	http.Redirect(w, r, "/carrinho", http.StatusSeeOther)
}

// ebookLoginURL leva ao login quando a sacola tem e-book: o livro fica na
// biblioteca da conta, e a volta é o próprio checkout.
const ebookLoginURL = "/conta/entrar?motivo=ebook&volta=%2Fcheckout"

// CheckoutView - formulário de checkout (GET)
func CheckoutView(w http.ResponseWriter, r *http.Request) {
	cart := getCart(r)
	if cart.Count() == 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if cart.HasDigital() && !isCustomerLoggedIn(r) {
		http.Redirect(w, r, ebookLoginURL, http.StatusSeeOther)
		return
	}
	data := map[string]any{
		"Cart":            cart,
		"CartCount":       cart.Count(),
		"PrecisaEndereco": cart.HasPhysical(),
		"TemDigital":      cart.HasDigital(),
	}
	switch r.URL.Query().Get("aviso") {
	case "dados":
		data["Aviso"] = "Preencha nome e e-mail para continuar."
	case "termos":
		data["Aviso"] = "Para finalizar a compra, aceite os Termos de Uso e a Política de Privacidade."
	case "endereco":
		data["Aviso"] = "Confira o endereço de entrega: CEP, rua, número, bairro, cidade e UF são obrigatórios."
	case "frete":
		data["Aviso"] = "Calcule o frete pelo CEP e escolha uma opção de entrega."
	case "cpf":
		data["Aviso"] = "Informe um CPF válido: ele vai na etiqueta de envio dos Correios."
	}
	if cart.HasPhysical() {
		if sellerID, nome, ok := cart.Seller(); ok {
			data["QuemEnvia"] = nome
			data["EnviaOSite"] = sellerID == 0
		}
	}
	if sellerID, _, ok := cart.Seller(); ok {
		if plan := installmentPreview(sellerID, cart.Total()); plan != nil {
			data["Parcelas"] = plan
		}
	}
	if isCustomerLoggedIn(r) {
		if c, err := models.GetCustomerByID(currentCustomerID(r)); err == nil {
			data["Customer"] = c
		}
	}
	renderStore(w, r, "checkout.html", data)
}

// CheckoutProcess - processa o pedido (POST)
func CheckoutProcess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/checkout", http.StatusSeeOther)
		return
	}
	r.ParseForm()
	cart := getCart(r)
	if cart.Count() == 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if cart.HasDigital() && !isCustomerLoggedIn(r) {
		http.Redirect(w, r, ebookLoginURL, http.StatusSeeOther)
		return
	}
	if !termsAccepted(r) {
		http.Redirect(w, r, "/checkout?aviso=termos", http.StatusSeeOther)
		return
	}
	if id := currentCustomerID(r); id > 0 && !models.HasCurrentConsent(models.SubjectCustomer, id) {
		recordConsent(r, models.SubjectCustomer, id, "checkout")
	}

	order := &models.Order{
		CustomerID:    currentCustomerID(r),
		CustomerName:  r.FormValue("nome"),
		CustomerEmail: r.FormValue("email"),
		CustomerPhone: r.FormValue("telefone"),
		Address:       r.FormValue("endereco"),
		Total:         cart.Total(),
	}
	for _, item := range cart.Items {
		order.Items = append(order.Items, models.OrderItem{
			ProductID:   item.Product.ID,
			ProductName: item.Product.Name,
			Quantity:    item.Quantity,
			Price:       item.UnitPrice(),
			Edition:     item.Edition,
		})
	}

	// Livro físico: endereço estruturado (a etiqueta exige) e frete cotado de
	// novo aqui, com o preço do Melhor Envio e não o do formulário.
	if cart.HasPhysical() {
		order.ShipTo = models.Address{
			PostalCode: r.FormValue("cep"), Street: r.FormValue("rua"), Number: r.FormValue("numero"),
			Complement: r.FormValue("complemento"), District: r.FormValue("bairro"),
			City: r.FormValue("cidade"), State: r.FormValue("uf"),
		}
		if err := order.ShipTo.Normalize(); err != nil {
			http.Redirect(w, r, "/checkout?aviso=endereco", http.StatusSeeOther)
			return
		}
		frete, err := shippingFromForm(r, cart, order.ShipTo.PostalCode)
		if err != nil {
			if !errors.Is(err, errFreteNaoEscolhido) {
				log.Printf("checkout: cotação do frete: %v", err)
			}
			http.Redirect(w, r, "/checkout?aviso=frete", http.StatusSeeOther)
			return
		}
		order.Shipping = frete
		// O CPF vai na etiqueta (declaração de conteúdo dos Correios).
		if frete.ServiceID > 0 {
			doc, err := models.ValidateDocument(r.FormValue("cpf"))
			if err != nil {
				http.Redirect(w, r, "/checkout?aviso=cpf", http.StatusSeeOther)
				return
			}
			order.CustomerDocument = doc
		}
	}

	// O meio de pagamento é escolhido no formulário; qualquer outro valor cai
	// no PIX, que não depende de URL pública configurada.
	method := r.FormValue("metodo_pagamento")
	if method != "cartao" {
		method = "pix"
	}

	id, err := models.CreateOrder(order)
	if err == nil {
		models.SetOrderTermsVersion(int(id))
	}
	if err != nil {
		log.Printf("checkout: CreateOrder: %v", err)
		switch {
		case errors.Is(err, models.ErrMixedSellers):
			http.Redirect(w, r, "/carrinho?aviso=vendedor", http.StatusSeeOther)
		case errors.Is(err, models.ErrSellerCannotReceive):
			http.Redirect(w, r, "/carrinho?aviso=indisponivel", http.StatusSeeOther)
		case errors.Is(err, models.ErrInsufficientStock):
			http.Redirect(w, r, "/carrinho?aviso=estoque", http.StatusSeeOther)
		case errors.Is(err, models.ErrDigitalNeedsAccount):
			http.Redirect(w, r, ebookLoginURL, http.StatusSeeOther)
		case errors.Is(err, models.ErrDigitalUnavailable):
			http.Redirect(w, r, "/carrinho?aviso=ebook-indisponivel", http.StatusSeeOther)
		case errors.Is(err, models.ErrEditionInvalid):
			http.Redirect(w, r, "/carrinho?aviso=ebook-indisponivel", http.StatusSeeOther)
		case errors.Is(err, models.ErrAlreadyOwned):
			http.Redirect(w, r, "/carrinho?aviso=ja-tem", http.StatusSeeOther)
		case errors.Is(err, models.ErrInvalidOrder):
			http.Redirect(w, r, "/checkout?aviso=dados", http.StatusSeeOther)
		case errors.Is(err, models.ErrInvalidAddress):
			http.Redirect(w, r, "/checkout?aviso=endereco", http.StatusSeeOther)
		default:
			http.Error(w, "não foi possível criar o pedido", http.StatusConflict)
		}
		return
	}
	rememberGuestOrder(w, r, int(id))
	cart.Clear()
	saveCart(w, r, cart)

	idStr := strconv.FormatInt(id, 10)
	if err := models.SetOrderPaymentMethod(id, method); err != nil {
		log.Printf("checkout: SetOrderPaymentMethod: %v", err)
	}

	// Livro do site: cobra na conta do site. Livro de vendedor: cobra na conta
	// do vendedor, com a comissão do site retida pelo Mercado Pago.
	accessToken, collector, fee, err := paymentAccountForOrder(order)
	if err != nil {
		log.Printf("checkout: pedido #%s sem conta para cobrar: %v", idStr, err)
		http.Redirect(w, r, "/pedido?id="+idStr, http.StatusSeeOther)
		return
	}
	if collector != "" {
		if err := models.SetOrderCollector(id, collector); err != nil {
			log.Printf("checkout: SetOrderCollector: %v", err)
		}
	}

	storeName := models.GetSetting("store_name")
	if storeName == "" {
		storeName = "Livredo"
	}
	baseURL := strings.TrimRight(models.GetSetting("store_base_url"), "/")
	notifURL := ""
	if baseURL != "" {
		notifURL = baseURL + "/webhook/mp"
	}

	if method == "cartao" {
		startCardCheckout(w, r, accessToken, order, id, idStr, baseURL, notifURL, storeName, fee)
		return
	}
	startPixCheckout(w, r, accessToken, order, id, idStr, notifURL, storeName, fee)
}

// startPixCheckout cria a cobrança PIX e leva o cliente à página do QR Code.
func startPixCheckout(w http.ResponseWriter, r *http.Request, accessToken string, order *models.Order, id int64, idStr, notifURL, storeName string, fee float64) {
	description := "Pedido #" + idStr + " - " + storeName
	pix, err := services.CreatePixPayment(accessToken, order.Total, description, id, order.CustomerEmail, notifURL, pixExpiration, fee)
	if err != nil {
		log.Printf("checkout: CreatePixPayment: %v", err)
		http.Redirect(w, r, "/pedido?id="+idStr, http.StatusSeeOther)
		return
	}
	if err := models.UpdateOrderPix(id, pix.ID, pix.QRCode, pix.QRCodeBase64); err != nil {
		log.Printf("checkout: UpdateOrderPix: %v", err)
	}
	http.Redirect(w, r, "/pix?pedido="+idStr, http.StatusSeeOther)
}

// startCardCheckout cria a preferência do Checkout Pro e redireciona o cliente
// ao ambiente do Mercado Pago, onde os dados do cartão são digitados. A loja
// nunca recebe nem armazena número de cartão.
func startCardCheckout(w http.ResponseWriter, r *http.Request, accessToken string, order *models.Order, id int64, idStr, baseURL, notifURL, storeName string, fee float64) {
	items := make([]services.PaymentItem, 0, len(order.Items)+1)
	for _, item := range order.Items {
		items = append(items, services.PaymentItem{
			Title:     item.ProductName,
			Quantity:  item.Quantity,
			UnitPrice: item.Price,
		})
	}
	// O frete entra como mais uma linha: o Checkout Pro cobra a soma dos itens.
	if order.Shipping.Cents > 0 {
		items = append(items, services.PaymentItem{
			Title:     "Frete — " + order.Shipping.Service,
			Quantity:  1,
			UnitPrice: order.Shipping.Reais(),
		})
	}

	preference, err := services.CreateCheckoutPreference(
		accessToken, items, id, order.CustomerName, order.CustomerEmail,
		baseURL, notifURL, storeName, maxInstallments, checkoutExpiration, fee,
	)
	if err != nil {
		log.Printf("checkout: CreateCheckoutPreference: %v", err)
		http.Redirect(w, r, "/pedido?id="+idStr, http.StatusSeeOther)
		return
	}
	if err := models.SetOrderCheckout(id, preference.ID, preference.InitPoint); err != nil {
		log.Printf("checkout: SetOrderCheckout: %v", err)
		http.Redirect(w, r, "/pedido?id="+idStr, http.StatusSeeOther)
		return
	}
	// Não redireciona direto para o Mercado Pago: a CSP "form-action 'self'"
	// faz o Chrome bloquear qualquer redirecionamento para outro site que
	// comece num envio de formulário. A página intermediária é do próprio site
	// e abre o Mercado Pago como uma navegação nova.
	http.Redirect(w, r, "/pagamento/ir?pedido="+idStr, http.StatusSeeOther)
}

// OrderConfirm - confirmação do pedido
func OrderConfirm(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	order, err := models.GetOrder(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !requireOrderAccess(w, r, order) {
		return
	}
	data := map[string]any{
		"Order":     order,
		"CartCount": 0,
		// Download só para o dono da conta que comprou, com o pedido pago.
		"PodeBaixar": order.IsPaid() && order.HasDigital() && order.CustomerID > 0 && order.CustomerID == currentCustomerID(r),
	}
	avisos := map[string]string{
		"enviado":      "Comprovante enviado para " + order.CustomerEmail + ".",
		"erro":         "Não foi possível enviar o e-mail agora. Tente de novo em instantes ou baixe o PDF.",
		"limite":       "O comprovante já foi enviado algumas vezes na última hora. Confira sua caixa de entrada e o spam.",
		"indisponivel": "O envio por e-mail ainda não está disponível nesta loja. Baixe o PDF.",
	}
	if aviso, ok := avisos[r.URL.Query().Get("email")]; ok {
		data["EmailAviso"] = aviso
		data["EmailOK"] = r.URL.Query().Get("email") == "enviado"
	}
	data["Despacho"] = models.GetDispatch(order.ID)
	if order.SellerID != 0 {
		if seller, err := models.GetSeller(order.SellerID); err == nil {
			data["Seller"] = seller
			data["SellerRating"] = models.GetSellerRating(seller.ID)
		}
		data["Review"] = models.GetReviewForOrder(order.ID)
		data["CanReview"] = models.CanReview(order, currentCustomerID(r))
		if aviso, ok := reviewAvisos[r.URL.Query().Get("avaliacao")]; ok {
			data["ReviewAviso"] = aviso
			data["ReviewOK"] = r.URL.Query().Get("avaliacao") == "ok"
		}
	}
	renderStore(w, r, "order_confirm.html", data)
}

func containsCI(s, sub string) bool {
	sl := len(s)
	subl := len(sub)
	if subl > sl {
		return false
	}
	for i := 0; i <= sl-subl; i++ {
		match := true
		for j := 0; j < subl; j++ {
			a, b := s[i+j], sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

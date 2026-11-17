package handlers

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vendas/loja/models"
)

// O painel do vendedor é deliberadamente estreito: cadastrar e acompanhar os
// próprios produtos e pedidos. Nada de configuração técnica — credenciais de
// pagamento, webhook e categorias ficam no painel do administrador.

// lowStockThreshold define quando o painel destaca um produto como acabando.
const lowStockThreshold = 5

func currentSeller(r *http.Request) *models.Seller {
	sess, _ := store.Get(r, "session")
	id, ok := sess.Values["seller_id"].(int)
	if !ok || id == 0 {
		return nil
	}
	seller, err := models.GetSeller(id)
	if err != nil || !seller.Active {
		return nil
	}
	return seller
}

func RequireSeller(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		seller := currentSeller(r)
		if seller == nil {
			http.Redirect(w, r, "/vendedor/login", http.StatusSeeOther)
			return
		}
		if r.URL.Path != "/vendedor/meus-dados.json" && !requireConsent(models.SubjectSeller, seller.ID, w, r) {
			return
		}
		next(w, r)
	}
}

// SellerRegister permite que o próprio vendedor crie sua conta, sem depender
// do administrador. A conta nasce ativa: o controle de quem pode vender é a
// senha da própria conta, assim como acontece no cadastro de cliente.
func SellerRegister(w http.ResponseWriter, r *http.Request) {
	if currentSeller(r) != nil {
		http.Redirect(w, r, "/vendedor", http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodPost {
		renderSeller(w, r, "seller_register.html", map[string]any{"Perfis": models.SellerProfiles})
		return
	}

	// Cada tentativa conta, aprovada ou não: diferente do login, aqui nunca há
	// Reset, para um script não conseguir criar contas sem limite.
	key := loginRateKey("seller-register", r)
	if allowed, retryIn := registerLimiter.Allowed(key); !allowed {
		renderSeller(w, r, "seller_register.html", map[string]any{"Error": blockedMessage(retryIn), "Perfis": models.SellerProfiles})
		return
	}
	registerLimiter.RegisterFailure(key)

	r.ParseForm()
	name := strings.TrimSpace(r.FormValue("nome"))
	username := strings.TrimSpace(r.FormValue("usuario"))
	email := strings.TrimSpace(r.FormValue("email"))
	document := r.FormValue("documento")
	password := r.FormValue("senha")
	confirm := r.FormValue("confirmar_senha")

	// Repopula o formulário com o que a pessoa já digitou, exceto a senha.
	perfil := r.FormValue("perfil")
	formValues := map[string]any{"Nome": name, "Usuario": username, "Email": email, "Documento": document,
		"Perfil": perfil, "Perfis": models.SellerProfiles}

	if !termsAccepted(r) {
		formValues["Error"] = "Para criar a conta, aceite os Termos de Uso e a Política de Privacidade."
		renderSeller(w, r, "seller_register.html", formValues)
		return
	}
	if !models.ValidSellerProfile(perfil) {
		formValues["Error"] = "Conte que tipo de vendedor você é."
		renderSeller(w, r, "seller_register.html", formValues)
		return
	}
	if password != confirm {
		formValues["Error"] = "As senhas não coincidem."
		renderSeller(w, r, "seller_register.html", formValues)
		return
	}

	if err := models.CreateSeller(name, username, email, document, password); err != nil {
		formValues["Error"] = sellerErrorMessage(err)
		renderSeller(w, r, "seller_register.html", formValues)
		return
	}
	sendWelcomeEmail(email, welcomeData{Nome: name, Vendedor: true, Usuario: username})

	// Autentica de novo em vez de assumir sucesso: o cadastro só grava o hash,
	// e é a autenticação que devolve o ID que a sessão precisa.
	seller, err := models.AuthenticateSeller(username, password)
	if err != nil {
		http.Redirect(w, r, "/vendedor/login", http.StatusSeeOther)
		return
	}
	recordConsent(r, models.SubjectSeller, seller.ID, "cadastro")
	if err := models.SetSellerProfile(seller.ID, perfil); err != nil {
		log.Printf("tipo do vendedor %d: %v", seller.ID, err)
	}

	sess, _ := store.Get(r, "session")
	if err := rotateCSRFToken(sess); err != nil {
		http.Error(w, "não foi possível iniciar a sessão", http.StatusInternalServerError)
		return
	}
	sess.Values["seller_id"] = seller.ID
	sess.Values["seller_name"] = seller.Name
	if err := sess.Save(r, w); err != nil {
		http.Error(w, "não foi possível salvar a sessão", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/vendedor", http.StatusSeeOther)
}

func SellerLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		renderSeller(w, r, "seller_login.html", nil)
		return
	}

	key := loginRateKey("seller", r)
	if allowed, retryIn := loginLimiter.Allowed(key); !allowed {
		renderSeller(w, r, "seller_login.html", map[string]any{
			"Error": blockedMessage(retryIn),
		})
		return
	}

	r.ParseForm()
	seller, err := models.AuthenticateSeller(r.FormValue("usuario"), r.FormValue("senha"))
	if err != nil {
		loginLimiter.RegisterFailure(key)
		// A mensagem não distingue usuário inexistente de senha errada, para
		// não confirmar quais contas existem.
		renderSeller(w, r, "seller_login.html", map[string]any{"Error": "Usuário ou senha inválidos"})
		return
	}

	loginLimiter.Reset(key)
	sess, _ := store.Get(r, "session")
	if err := rotateCSRFToken(sess); err != nil {
		http.Error(w, "não foi possível iniciar a sessão", http.StatusInternalServerError)
		return
	}
	sess.Values["seller_id"] = seller.ID
	sess.Values["seller_name"] = seller.Name
	if err := sess.Save(r, w); err != nil {
		http.Error(w, "não foi possível salvar a sessão", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/vendedor", http.StatusSeeOther)
}

func SellerLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	sess, _ := store.Get(r, "session")
	delete(sess.Values, "seller_id")
	delete(sess.Values, "seller_name")
	_ = rotateCSRFToken(sess)
	sess.Save(r, w)
	http.Redirect(w, r, "/vendedor/login", http.StatusSeeOther)
}

// SellerDashboard resume o que o vendedor precisa ver ao entrar: quanto vendeu,
// o que está para enviar e o que está acabando no estoque.
func SellerDashboard(w http.ResponseWriter, r *http.Request) {
	seller := currentSeller(r)
	orders, err := models.ListOrdersBySeller(seller.ID)
	if err != nil {
		log.Printf("painel do vendedor: ListOrdersBySeller: %v", err)
	}
	products, err := models.ListProductsBySeller(seller.ID)
	if err != nil {
		log.Printf("painel do vendedor: ListProductsBySeller: %v", err)
	}

	var revenue float64
	var awaitingShipment int
	for _, order := range orders {
		// Só conta o que foi efetivamente pago: pedido pendente não é receita.
		switch order.Status {
		case "pago", models.StatusReady:
			revenue += order.Total
			awaitingShipment++
		case "enviado", "entregue":
			revenue += order.Total
		}
	}

	var lowStock []models.Product
	var activeProducts int
	for _, product := range products {
		if product.Active {
			activeProducts++
			// E-book não tem estoque: nunca "acaba".
			if !product.IsDigital() && product.Stock <= lowStockThreshold {
				lowStock = append(lowStock, product)
			}
		}
	}

	recent := orders
	if len(recent) > 5 {
		recent = recent[:5]
	}

	renderSeller(w, r, "seller_dashboard.html", map[string]any{
		"MP":               sellerMPPanel(r),
		"Seller":           seller,
		"RecentOrders":     recent,
		"TotalOrders":      len(orders),
		"TotalProducts":    activeProducts,
		"TotalRevenue":     revenue,
		"AwaitingShipment": awaitingShipment,
		"LowStock":         lowStock,
		"Rating":           models.GetSellerRating(seller.ID),
		"Perfis":           models.SellerProfiles,
		"PerfilSalvo":      r.URL.Query().Get("perfil") == "salvo",
		"GoogleDisponivel": googleLoginAvailable(),
		"GoogleVinculado":  models.SellerGoogleLinked(seller.ID),
		"GoogleAviso":      sellerGoogleAvisos[r.URL.Query().Get("google")].texto,
		"GoogleAvisoOK":    sellerGoogleAvisos[r.URL.Query().Get("google")].ok,
		"Comissao":         models.GetSellerCommission(seller.ID),
		"Beneficios":       models.TierBenefits(),
		"MinAvaliacoes":    models.MinRatedSalesForDiscount,
		"FotoAviso":        avatarAvisos[r.URL.Query().Get("foto")],
		"FotoOK":           r.URL.Query().Get("foto") == "ok" || r.URL.Query().Get("foto") == "removida",
		"Envios":           models.GetShippingStats(seller.ID),
		"Pausa":            models.GetSellerPause(seller.ID),
		"SLA":              models.SLALabel(models.SLAHours(seller.ID)),
	})
}

func SellerProducts(w http.ResponseWriter, r *http.Request) {
	seller := currentSeller(r)
	products, err := models.ListProductsBySeller(seller.ID)
	if err != nil {
		log.Printf("painel do vendedor: ListProductsBySeller: %v", err)
	}
	renderSeller(w, r, "seller_products.html", map[string]any{
		"MP":                sellerMPPanel(r),
		"Seller":            seller,
		"Products":          products,
		"LowStockThreshold": lowStockThreshold,
	})
}

func SellerProductNew(w http.ResponseWriter, r *http.Request) {
	seller := currentSeller(r)
	if r.Method != http.MethodPost {
		renderSeller(w, r, "seller_product_form.html", map[string]any{
			"Seller":     seller,
			"Categories": categoriesForForm(),
			"Direitos":   models.GetDigitalRights(seller.ID),
		})
		return
	}

	if err := r.ParseMultipartForm(maxImageBytes); err != nil {
		renderSellerProductForm(w, r, seller, nil, "Não foi possível ler o formulário. Verifique o tamanho da imagem.")
		return
	}

	imageURL, err := resolveProductImage(r, "")
	if err != nil {
		renderSellerProductForm(w, r, seller, nil, imageErrorMessage(err))
		return
	}

	product := models.Product{
		SellerID:    seller.ID,
		Name:        r.FormValue("nome"),
		Description: r.FormValue("descricao"),
		Price:       parsePrice(r.FormValue("preco")),
		Stock:       parseStock(r.FormValue("estoque")),
		ImageURL:    imageURL,
		Category:    r.FormValue("categoria"),
		Condition:   r.FormValue("estado"),
		Active:      true,
	}
	novoEbook, _, err := applyBookForm(r, &product, nil)
	if err != nil {
		renderSellerProductForm(w, r, seller, &product, ebookErrorMessage(err))
		return
	}
	if err := models.CreateProduct(product); err != nil {
		removeEbookFile(novoEbook)
		renderSellerProductForm(w, r, seller, &product, productErrorMessage(err))
		return
	}
	http.Redirect(w, r, "/vendedor/produtos", http.StatusSeeOther)
}

func SellerProductEdit(w http.ResponseWriter, r *http.Request) {
	seller := currentSeller(r)
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	product, err := models.GetProduct(id)
	if err != nil || product.SellerID != seller.ID {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodPost {
		renderSellerProductForm(w, r, seller, product, "")
		return
	}

	if err := r.ParseMultipartForm(maxImageBytes); err != nil {
		renderSellerProductForm(w, r, seller, product, "Não foi possível ler o formulário. Verifique o tamanho da imagem.")
		return
	}

	imageURL, err := resolveProductImage(r, product.ImageURL)
	if err != nil {
		renderSellerProductForm(w, r, seller, product, imageErrorMessage(err))
		return
	}

	updated := models.Product{
		ID:          product.ID,
		SellerID:    seller.ID,
		Name:        r.FormValue("nome"),
		Description: r.FormValue("descricao"),
		Price:       parsePrice(r.FormValue("preco")),
		Stock:       parseStock(r.FormValue("estoque")),
		ImageURL:    imageURL,
		Category:    r.FormValue("categoria"),
		Condition:   r.FormValue("estado"),
		Active:      r.FormValue("ativo") == "on",
	}
	novoEbook, antigoEbook, err := applyBookForm(r, &updated, product)
	if err != nil {
		renderSellerProductForm(w, r, seller, &updated, ebookErrorMessage(err))
		return
	}
	// A gravação reconfirma o dono no próprio UPDATE, de modo que um id forjado
	// no formulário não alcance o produto de outro vendedor.
	if err := models.UpdateProductBySeller(updated, seller.ID); err != nil {
		removeEbookFile(novoEbook)
		renderSellerProductForm(w, r, seller, &updated, productErrorMessage(err))
		return
	}
	if antigoEbook != novoEbook {
		removeEbookFile(antigoEbook)
	}
	http.Redirect(w, r, "/vendedor/produtos", http.StatusSeeOther)
}

func SellerProductDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	seller := currentSeller(r)
	id, _ := strconv.Atoi(r.FormValue("id"))
	if product, err := models.GetProduct(id); err == nil && product.SellerID == seller.ID {
		if err := deleteProduct(id); err != nil {
			log.Printf("painel do vendedor: exclusão do produto %d: %v", id, err)
		}
	}
	http.Redirect(w, r, "/vendedor/produtos", http.StatusSeeOther)
}

func SellerOrders(w http.ResponseWriter, r *http.Request) {
	seller := currentSeller(r)
	orders, err := models.ListOrdersBySeller(seller.ID)
	if err != nil {
		log.Printf("painel do vendedor: ListOrdersBySeller: %v", err)
	}
	renderSeller(w, r, "seller_orders.html", map[string]any{"Seller": seller, "Orders": orders})
}

func SellerOrderDetail(w http.ResponseWriter, r *http.Request) {
	seller := currentSeller(r)
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	belongs, err := models.OrderBelongsToSeller(id, seller.ID)
	if err != nil || !belongs {
		http.NotFound(w, r)
		return
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		// Etiqueta e rastreio: gerados na conta Melhor Envio do vendedor.
		if r.FormValue("acao") != "" {
			order, err := models.GetOrderForSeller(id, seller.ID)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			if msg := handleLabelAction(r, order, seller.ID); msg != "" {
				renderSeller(w, r, "seller_order_detail.html", sellerOrderData(seller, id, msg))
				return
			}
			http.Redirect(w, r, "/vendedor/pedidos/detalhe?id="+strconv.Itoa(id), http.StatusSeeOther)
			return
		}
		// O vendedor só movimenta a entrega. Marcar como pago continua
		// exclusivo do webhook do provedor.
		status := r.FormValue("status")
		if status == "enviado" && strings.TrimSpace(r.FormValue("codigo")) != "" {
			models.SetOrderTracking(id, r.FormValue("codigo"))
		}
		if err := models.UpdateSellerOrderStatus(id, status); err != nil {
			order, getErr := models.GetOrderForSeller(id, seller.ID)
			if getErr != nil {
				http.NotFound(w, r)
				return
			}
			renderSeller(w, r, "seller_order_detail.html", map[string]any{
				"Seller":   seller,
				"Order":    order,
				"Error":    "Esse passo não vale para a situação atual do pedido.",
				"Despacho": models.GetDispatch(id),
			})
			return
		}
		notifyBuyer(id, status)
		models.ForgetShippingBadges()
		http.Redirect(w, r, "/vendedor/pedidos/detalhe?id="+strconv.Itoa(id), http.StatusSeeOther)
		return
	}

	data := sellerOrderData(seller, id, "")
	if data == nil {
		http.NotFound(w, r)
		return
	}
	renderSeller(w, r, "seller_order_detail.html", data)
}

// sellerOrderData monta a página do pedido do vendedor, com o estado do envio.
func sellerOrderData(seller *models.Seller, orderID int, errMsg string) map[string]any {
	order, err := models.GetOrderForSeller(orderID, seller.ID)
	if err != nil {
		return nil
	}
	data := map[string]any{
		"Seller":   seller,
		"Order":    order,
		"Origem":   models.GetShippingOrigin(seller.ID),
		"Despacho": models.GetDispatch(orderID),
		"SLA":      models.SLALabel(models.SLAHours(seller.ID)),
	}
	if errMsg != "" {
		data["Error"] = errMsg
	}
	return data
}

// ── Apoio ───────────────────────────────────────────────────────────────────

func renderSellerProductForm(w http.ResponseWriter, r *http.Request, seller *models.Seller, product *models.Product, errMsg string) {
	data := map[string]any{
		"Seller":     seller,
		"Categories": categoriesForForm(),
		"Direitos":   models.GetDigitalRights(seller.ID),
	}
	if product != nil {
		data["Product"] = product
	}
	if errMsg != "" {
		data["Error"] = errMsg
	}
	renderSeller(w, r, "seller_product_form.html", data)
}

func categoriesForForm() []string {
	names, err := models.ListCategoryNames()
	if err != nil {
		log.Printf("categorias: %v", err)
		return []string{models.DefaultCategory()}
	}
	return names
}

// parsePrice aceita tanto 1234.56 quanto 1234,56, que é como o teclado
// brasileiro digita.
func parsePrice(raw string) float64 {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, ",", "."))
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0
	}
	return value
}

func parseStock(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return value
}

func imageErrorMessage(err error) string {
	if errors.Is(err, ErrInvalidImage) {
		return "Envie uma imagem JPEG, PNG, WebP ou GIF de até 4 MB."
	}
	log.Printf("upload de imagem: %v", err)
	return "Não foi possível salvar a imagem. Tente novamente."
}

func productErrorMessage(err error) string {
	if errors.Is(err, models.ErrInvalidProduct) {
		if _, message, found := strings.Cut(err.Error(), ": "); found {
			return capitalizeFirst(message) + "."
		}
		return "Confira os dados do produto."
	}
	log.Printf("gravação de produto: %v", err)
	return "Não foi possível salvar o produto. Tente novamente."
}

// sellerErrorMessage traduz os erros de CreateSeller para o que a pessoa
// digitou errado, sem nunca vazar o erro cru do banco.
func sellerErrorMessage(err error) string {
	switch {
	case errors.Is(err, models.ErrUsernameInUse):
		return "Este usuário já está em uso. Escolha outro."
	case errors.Is(err, models.ErrEmailInUse):
		return "Este e-mail já está cadastrado para outro vendedor."
	case errors.Is(err, models.ErrDocumentInUse):
		return "Este CPF/CNPJ já está cadastrado para outro vendedor."
	case errors.Is(err, models.ErrInvalidSeller):
		if _, message, found := strings.Cut(err.Error(), ": "); found {
			return capitalizeFirst(message) + "."
		}
		return "Confira os dados informados."
	default:
		log.Printf("cadastro de vendedor: %v", err)
		return "Não foi possível criar a conta. Tente novamente."
	}
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func blockedMessage(retryIn time.Duration) string {
	minutes := int(retryIn.Minutes()) + 1
	return "Muitas tentativas de login. Tente novamente em " + strconv.Itoa(minutes) + " minuto(s)."
}

// mpPanel reúne o que o painel mostra sobre recebimentos.
type mpPanel struct {
	Comissao   string // percentual, já formatado
	Disponivel bool   // o admin já ativou a conexão com o Mercado Pago
	Aviso      string
	AvisoOK    bool
}

var mpAvisos = map[string]struct {
	texto string
	ok    bool
}{
	"conectado":     {"Conta do Mercado Pago conectada! Seus livros já aparecem na loja.", true},
	"desconectado":  {"Conta desconectada. Seus livros saíram da loja até você conectar de novo.", true},
	"negado":        {"Você cancelou a autorização no Mercado Pago. Nada foi alterado.", false},
	"erro":          {"Não foi possível conectar sua conta. Tente de novo em instantes.", false},
	"conta-do-site": {"Essa é a conta do próprio site. Conecte a sua conta pessoal ou a da sua loja.", false},
	"indisponivel":  {"A conexão com o Mercado Pago ainda não foi ativada pelo administrador do site.", false},
}

func sellerMPPanel(r *http.Request) mpPanel {
	comissao := models.MarketplaceFeePercent()
	if seller := currentSeller(r); seller != nil {
		comissao = models.SellerFeePercent(seller.ID)
	}
	panel := mpPanel{
		Comissao: models.PercentLabel(comissao),
		Disponivel: models.GetSetting("mp_client_id") != "" &&
			models.GetSetting("mp_client_secret") != "" && mpRedirectURI() != "",
	}
	if aviso, ok := mpAvisos[r.URL.Query().Get("mp")]; ok {
		panel.Aviso, panel.AvisoOK = aviso.texto, aviso.ok
	}
	return panel
}

// SellerProfileUpdate - POST /vendedor/perfil: muda o tipo de vendedor
// (também para quem se cadastrou antes de o campo existir).
func SellerProfileUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	seller := currentSeller(r)
	if err := models.SetSellerProfile(seller.ID, r.FormValue("perfil")); err != nil {
		http.Redirect(w, r, "/vendedor", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/vendedor?perfil=salvo#perfil", http.StatusSeeOther)
}

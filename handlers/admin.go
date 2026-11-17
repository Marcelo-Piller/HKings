package handlers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/vendas/loja/models"
	"github.com/vendas/loja/services"
)

func isAdminLoggedIn(r *http.Request) bool {
	sess, _ := store.Get(r, "session")
	v, ok := sess.Values["admin"]
	if !ok {
		return false
	}
	logged, _ := v.(bool)
	return logged
}

// adminUsername retorna o usuário admin logado na sessão, ou "" se não logado.
func adminUsername(r *http.Request) string {
	sess, _ := store.Get(r, "session")
	v, ok := sess.Values["admin_username"]
	if !ok {
		return ""
	}
	name, _ := v.(string)
	return name
}

// RequireAdmin é o middleware de proteção das rotas admin.
func RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAdminLoggedIn(r) {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// AdminLogin - página de login
func AdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		renderAdmin(w, r, "admin_login.html", nil)
		return
	}

	key := loginRateKey("admin", r)
	if allowed, retryIn := loginLimiter.Allowed(key); !allowed {
		renderAdmin(w, r, "admin_login.html", map[string]any{"Error": blockedMessage(retryIn)})
		return
	}

	r.ParseForm()
	user := r.FormValue("usuario")
	pass := r.FormValue("senha")

	if err := models.AuthenticateAdmin(user, pass); err != nil {
		loginLimiter.RegisterFailure(key)
		log.Printf("login de admin recusado para %q vindo de %s", user, clientIP(r))
		renderAdmin(w, r, "admin_login.html", map[string]any{"Error": "Usuário ou senha inválidos"})
		return
	}

	loginLimiter.Reset(key)
	sess, _ := store.Get(r, "session")
	// Token novo a cada elevação de privilégio: um token obtido antes do login
	// não deve continuar válido depois dele.
	if err := rotateCSRFToken(sess); err != nil {
		http.Error(w, "não foi possível iniciar a sessão", http.StatusInternalServerError)
		return
	}
	sess.Values["admin"] = true
	sess.Values["admin_username"] = user
	if err := sess.Save(r, w); err != nil {
		http.Error(w, "não foi possível salvar a sessão", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// AdminLogout - logout
func AdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	sess, _ := store.Get(r, "session")
	delete(sess.Values, "admin")
	delete(sess.Values, "admin_username")
	_ = rotateCSRFToken(sess)
	sess.Save(r, w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// AdminDashboard - painel principal
func AdminDashboard(w http.ResponseWriter, r *http.Request) {
	orders, _ := models.ListOrders()
	products, _ := models.ListAllProducts()

	var totalRevenue float64
	for _, o := range orders {
		if o.Status != "cancelado" {
			totalRevenue += o.Total
		}
	}

	renderAdmin(w, r, "admin_dashboard.html", map[string]any{
		"Orders":        orders,
		"Products":      products,
		"TotalOrders":   len(orders),
		"TotalRevenue":  totalRevenue,
		"TotalProducts": len(products),
		"MPPendente":    mpOAuthMissing(),
		"LGPDPendente":  models.GetSetting("lgpd_email") == "" || models.GetSetting("lgpd_controlador") == "",
		"Atrasados":     models.ListOverdueOrders(),
		"Pausados":      models.ListPausedSellers(),
	})
}

// mpOAuthMissing lista o que falta para os vendedores conectarem sozinhos a
// conta do Mercado Pago. É configuração única do site, não aprovação por vendedor.
func mpOAuthMissing() []string {
	var missing []string
	if models.GetSetting("mp_client_id") == "" {
		missing = append(missing, "Client ID")
	}
	if models.GetSetting("mp_client_secret") == "" {
		missing = append(missing, "Client Secret")
	}
	if mpRedirectURI() == "" {
		missing = append(missing, "URL Base da Loja")
	}
	return missing
}

// AdminProducts - lista de produtos
func AdminProducts(w http.ResponseWriter, r *http.Request) {
	products, _ := models.ListAllProducts()
	renderAdmin(w, r, "admin_products.html", map[string]any{"Products": products})
}

// AdminProductNew - formulário novo produto
func AdminProductNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		renderAdminProductForm(w, r, nil, "")
		return
	}

	if err := r.ParseMultipartForm(maxImageBytes); err != nil {
		renderAdminProductForm(w, r, nil, "Não foi possível ler o formulário. Verifique o tamanho da imagem.")
		return
	}

	imageURL, err := resolveProductImage(r, "")
	if err != nil {
		renderAdminProductForm(w, r, nil, imageErrorMessage(err))
		return
	}

	sellerID, _ := strconv.Atoi(r.FormValue("seller_id"))
	p := models.Product{
		SellerID:    sellerID,
		Name:        r.FormValue("nome"),
		Description: r.FormValue("descricao"),
		Price:       parsePrice(r.FormValue("preco")),
		Stock:       parseStock(r.FormValue("estoque")),
		ImageURL:    imageURL,
		Category:    r.FormValue("categoria"),
		Condition:   r.FormValue("estado"),
		Active:      true,
	}
	novoEbook, _, err := applyBookForm(r, &p, nil)
	if err != nil {
		renderAdminProductForm(w, r, &p, ebookErrorMessage(err))
		return
	}
	if err := models.CreateProduct(p); err != nil {
		removeEbookFile(novoEbook)
		renderAdminProductForm(w, r, &p, productErrorMessage(err))
		return
	}
	http.Redirect(w, r, "/admin/produtos", http.StatusSeeOther)
}

// AdminProductEdit - editar produto
func AdminProductEdit(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	current, err := models.GetProduct(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodPost {
		renderAdminProductForm(w, r, current, "")
		return
	}

	if err := r.ParseMultipartForm(maxImageBytes); err != nil {
		renderAdminProductForm(w, r, current, "Não foi possível ler o formulário. Verifique o tamanho da imagem.")
		return
	}

	imageURL, err := resolveProductImage(r, current.ImageURL)
	if err != nil {
		renderAdminProductForm(w, r, current, imageErrorMessage(err))
		return
	}

	sellerID, _ := strconv.Atoi(r.FormValue("seller_id"))
	p := models.Product{
		ID:          id,
		SellerID:    sellerID,
		Name:        r.FormValue("nome"),
		Description: r.FormValue("descricao"),
		Price:       parsePrice(r.FormValue("preco")),
		Stock:       parseStock(r.FormValue("estoque")),
		ImageURL:    imageURL,
		Category:    r.FormValue("categoria"),
		Condition:   r.FormValue("estado"),
		Active:      r.FormValue("ativo") == "on",
	}
	novoEbook, antigoEbook, err := applyBookForm(r, &p, current)
	if err != nil {
		renderAdminProductForm(w, r, &p, ebookErrorMessage(err))
		return
	}
	if err := models.UpdateProduct(p); err != nil {
		removeEbookFile(novoEbook)
		renderAdminProductForm(w, r, &p, productErrorMessage(err))
		return
	}
	if antigoEbook != novoEbook {
		removeEbookFile(antigoEbook)
	}
	http.Redirect(w, r, "/admin/produtos", http.StatusSeeOther)
}

func renderAdminProductForm(w http.ResponseWriter, r *http.Request, product *models.Product, errMsg string) {
	data := map[string]any{
		"Product":    product,
		"Sellers":    sellersForAdmin(),
		"Categories": categoriesForForm(),
	}
	if errMsg != "" {
		data["Error"] = errMsg
	}
	renderAdmin(w, r, "admin_product_form.html", data)
}

// AdminProductDelete - desativa produto
func AdminProductDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/produtos", http.StatusSeeOther)
		return
	}
	r.ParseForm()
	id, _ := strconv.Atoi(r.FormValue("id"))
	if err := deleteProduct(id); err != nil {
		log.Printf("admin: exclusão do produto %d: %v", id, err)
	}
	http.Redirect(w, r, "/admin/produtos", http.StatusSeeOther)
}

// AdminOrders - lista de pedidos
func AdminOrders(w http.ResponseWriter, r *http.Request) {
	orders, _ := models.ListOrders()
	renderAdmin(w, r, "admin_orders.html", map[string]any{"Orders": orders})
}

// AdminOrderDetail - detalhes de um pedido
func AdminOrderDetail(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	order, err := models.GetOrder(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		// Etiqueta só nos pedidos do próprio site; os de vendedor são do vendedor.
		if r.FormValue("acao") != "" && order.SellerID == 0 {
			if msg := handleLabelAction(r, order, 0); msg != "" {
				renderAdmin(w, r, "admin_order_detail.html", map[string]any{
					"Order": order, "Recebedor": payeeName(order), "Origem": models.GetShippingOrigin(0), "Error": msg,
				})
				return
			}
			http.Redirect(w, r, "/admin/pedidos/detalhe?id="+strconv.Itoa(id), http.StatusSeeOther)
			return
		}
		// O status vinha do formulário direto para o banco sem conferência do
		// resultado; agora um valor recusado devolve o erro em vez de fingir
		// que a alteração ocorreu.
		status := r.FormValue("status")
		if status == "enviado" && strings.TrimSpace(r.FormValue("codigo")) != "" {
			models.SetOrderTracking(id, r.FormValue("codigo"))
		}
		if err := models.UpdateOrderStatus(id, status); err != nil {
			renderAdmin(w, r, "admin_order_detail.html", map[string]any{
				"Order":     order,
				"Recebedor": payeeName(order),
				"Error":     map[bool]string{true: "Este pedido está cancelado e o estoque já foi devolvido; ele não pode voltar para outra situação.", false: "Status inválido para este pedido."}[errors.Is(err, models.ErrOrderCancelled)],
			})
			return
		}
		notifyBuyer(id, status)
		http.Redirect(w, r, "/admin/pedidos/detalhe?id="+strconv.Itoa(id), http.StatusSeeOther)
		return
	}
	renderAdmin(w, r, "admin_order_detail.html", map[string]any{
		"Order": order, "Recebedor": payeeName(order), "Origem": models.GetShippingOrigin(0),
		"Despacho": models.GetDispatch(id), "SLA": models.SLALabel(models.SLAHours(order.SellerID)),
	})
}

// payeeName diz quem recebe o pagamento do pedido.
func payeeName(order *models.Order) string {
	if order.SellerID == 0 {
		return "Livredo (100% do valor)"
	}
	if seller, err := models.GetSeller(order.SellerID); err == nil {
		return seller.Name
	}
	return fmt.Sprintf("vendedor #%d", order.SellerID)
}

// settingKeys são as configurações editáveis pelo painel. Os segredos ficam
// marcados para nunca voltarem preenchidos ao formulário.
var settingKeys = []string{
	"mp_access_token", "mp_webhook_secret", "mp_client_id", "mp_client_secret",
	"comissao_percentual", "store_base_url", "store_name",
	"whatsapp_phone", "whatsapp_api_key",
	"smtp_host", "smtp_port", "smtp_user", "smtp_password",
	"google_client_id", "google_client_secret", "google_books_api_key",
	"lgpd_controlador", "lgpd_documento", "lgpd_encarregado", "lgpd_email",
	"sla_loja_horas", "sla_pessoa_horas",
	"msg_pronto_assunto", "msg_pronto_texto", "msg_enviado_assunto", "msg_enviado_texto", "msg_entregue_assunto", "msg_entregue_texto",
	"desconto_bronze", "desconto_prata", "desconto_ouro", "desconto_esmeralda",
	"melhorenvio_client_id", "melhorenvio_client_secret", "melhorenvio_sandbox", "melhorenvio_contato",
}

var secretFormFields = map[string]bool{
	"mp_access_token":   true,
	"mp_webhook_secret": true,
	"mp_client_id":      true,
	"mp_client_secret":  true,
	"whatsapp_api_key":  true,
	"smtp_password":     true,
	// O Client ID do Google não é segredo (aparece na URL de login), então
	// fica visível; só a chave secreta é protegida.
	"google_client_secret":      true,
	"google_books_api_key":      true,
	"melhorenvio_client_secret": true,
}

// AdminSettings - configurações da loja (Mercado Pago + WhatsApp)
func AdminSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		for _, campo := range []string{"comissao_percentual", "desconto_bronze", "desconto_prata", "desconto_ouro", "desconto_esmeralda"} {
			if pct := strings.TrimSpace(strings.ReplaceAll(r.FormValue(campo), ",", ".")); pct != "" {
				if v, err := strconv.ParseFloat(pct, 64); err != nil || v < 0 || v > 50 {
					renderAdminSettings(w, r, false, "A comissão e os descontos precisam ser números entre 0 e 50.")
					return
				}
			}
		}
		// O navegador pode preencher sozinho o campo de senha do token com a
		// senha de login; só aceita valor com formato de Access Token.
		if t := strings.TrimSpace(r.FormValue("mp_access_token")); t != "" {
			if k := services.ClassifyMPToken(t); k.Tipo != "producao" && k.Tipo != "teste" {
				renderAdminSettings(w, r, false, "Access Token do Mercado Pago não salvo: "+k.Mensagem+" Se você não digitou nada, o navegador preencheu o campo sozinho; apague e salve de novo.")
				return
			}
		}
		if t := strings.TrimSpace(r.FormValue("mp_webhook_secret")); t != "" && !services.ValidMPWebhookSecret(t) {
			renderAdminSettings(w, r, false, "Assinatura secreta do webhook não salva: ela é um código de letras a–f e números (normalmente 64 caracteres), em Mercado Pago → Suas integrações → Webhooks. Se você não digitou nada, o navegador preencheu o campo sozinho; apague e salve de novo.")
			return
		}
		for _, k := range settingKeys {
			value := strings.TrimSpace(r.FormValue(k))
			// Campo de segredo em branco significa "manter o valor atual", e
			// não "apagar": o formulário nunca exibe o segredo gravado.
			if secretFormFields[k] && value == "" {
				continue
			}
			if err := models.SetSetting(k, value); err != nil {
				log.Printf("configurações: %s: %v", k, err)
				renderAdminSettings(w, r, false, "Não foi possível salvar. Confirme se SETTINGS_ENCRYPTION_KEY está configurada.")
				return
			}
		}
		http.Redirect(w, r, "/admin/configuracoes?saved=1", http.StatusSeeOther)
		return
	}
	renderAdminSettings(w, r, r.URL.Query().Get("saved") == "1", "")
}

func renderAdminSettings(w http.ResponseWriter, r *http.Request, saved bool, errMsg string, extra ...map[string]any) {
	baseURL := models.GetSetting("store_base_url")
	data := map[string]any{
		"Saved":             saved,
		"StoreBaseURL":      baseURL,
		"StoreName":         models.GetSetting("store_name"),
		"WhatsAppPhone":     models.GetSetting("whatsapp_phone"),
		"WebhookURL":        strings.TrimRight(baseURL, "/") + "/webhook/mp",
		"HasAccessToken":    models.GetSetting("mp_access_token") != "",
		"HasWebhookSecret":  models.GetSetting("mp_webhook_secret") != "",
		"HasWhatsAppKey":    models.GetSetting("whatsapp_api_key") != "",
		"HasClientID":       models.GetSetting("mp_client_id") != "",
		"HasClientSecret":   models.GetSetting("mp_client_secret") != "",
		"Comissao":          strconv.FormatFloat(models.MarketplaceFeePercent(), 'f', -1, 64),
		"Beneficios":        models.TierBenefits(),
		"MinAvaliacoes":     models.MinRatedSalesForDiscount,
		"OAuthRedirectURL":  mpRedirectURI(),
		"SMTP":              smtpConfig(),
		"HasSMTPPassword":   models.GetSetting("smtp_password") != "",
		"GoogleClientID":    models.GetSetting("google_client_id"),
		"HasGoogleSecret":   models.GetSetting("google_client_secret") != "",
		"HasBooksKey":       models.GetSetting("google_books_api_key") != "",
		"LGPDControlador":   models.GetSetting("lgpd_controlador"),
		"LGPDDocumento":     models.GetSetting("lgpd_documento"),
		"LGPDEncarregado":   models.GetSetting("lgpd_encarregado"),
		"LGPDEmail":         models.GetSetting("lgpd_email"),
		"GoogleRedirectURL": googleRedirectURI(),
		"MEClientID":        models.GetSetting("melhorenvio_client_id"),
		"HasMESecret":       models.GetSetting("melhorenvio_client_secret") != "",
		"MESandbox":         models.GetSetting("melhorenvio_sandbox") == "1",
		"MEContato":         models.GetSetting("melhorenvio_contato"),
		"MERedirectURL":     meRedirectURI(),
	}
	if errMsg != "" {
		data["Error"] = errMsg
	}
	if r.URL.Query().Get("testar") == "mp" {
		data["TesteMP"] = testMercadoPago()
	}
	data["BoasVindas"] = currentWelcomeBatch()
	data["SLALoja"], data["SLAPessoa"] = models.GetSetting("sla_loja_horas"), models.GetSetting("sla_pessoa_horas")
	var avisos []map[string]string
	for i := range buyerNotices {
		n := &buyerNotices[i]
		subj, body := noticeText(n)
		avisos = append(avisos, map[string]string{"Label": n.Label, "SubjKey": n.SubjKey, "BodyKey": n.BodyK, "Subject": subj, "Body": body})
	}
	data["AvisosCompra"] = avisos
	for _, e := range extra {
		for k, v := range e {
			data[k] = v
		}
	}
	renderAdmin(w, r, "admin_settings.html", data)
}

// AdminCategories administra a lista de categorias que os formulários de
// produto oferecem. Centralizar aqui é o que tira o campo de texto livre do
// painel do vendedor.
func AdminCategories(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		var err error
		switch r.FormValue("acao") {
		case "excluir":
			id, _ := strconv.Atoi(r.FormValue("id"))
			err = models.DeleteCategory(id)
		default:
			err = models.CreateCategory(r.FormValue("nome"))
		}
		if err != nil {
			renderAdminCategories(w, r, categoryErrorMessage(err))
			return
		}
		http.Redirect(w, r, "/admin/categorias", http.StatusSeeOther)
		return
	}
	renderAdminCategories(w, r, "")
}

func renderAdminCategories(w http.ResponseWriter, r *http.Request, errMsg string) {
	categories, err := models.ListCategoriesFull()
	if err != nil {
		log.Printf("categorias: %v", err)
	}
	data := map[string]any{"Categories": categories}
	if errMsg != "" {
		data["Error"] = errMsg
	}
	renderAdmin(w, r, "admin_categories.html", data)
}

func categoryErrorMessage(err error) string {
	switch {
	case errors.Is(err, models.ErrCategoryInUse):
		return "Essa categoria ainda tem produtos. Mova os produtos antes de excluí-la."
	case errors.Is(err, models.ErrInvalidCategory):
		return "Informe um nome válido e que ainda não exista."
	default:
		log.Printf("categorias: %v", err)
		return "Não foi possível concluir a operação."
	}
}

func sellersForAdmin() []models.Seller {
	sellers, _ := models.ListSellers()
	return sellers
}

func AdminSellers(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		err := models.CreateSeller(
			r.FormValue("nome"), r.FormValue("usuario"),
			r.FormValue("email"), r.FormValue("documento"),
			r.FormValue("senha"),
		)
		if err != nil {
			renderAdmin(w, r, "admin_sellers.html", map[string]any{
				"Sellers": sellersForAdmin(), "Error": sellerErrorMessage(err),
				"Nome": r.FormValue("nome"), "Usuario": r.FormValue("usuario"),
				"Email": r.FormValue("email"), "Documento": r.FormValue("documento"),
			})
			return
		}
		http.Redirect(w, r, "/admin/vendedores", http.StatusSeeOther)
		return
	}
	renderAdmin(w, r, "admin_sellers.html", map[string]any{"Sellers": sellersForAdmin()})
}

// mpTestResult é o resultado do botão "Testar conexão" do Mercado Pago.
type mpTestResult struct {
	Formato services.MPTokenKind
	Origem  string // "painel" ou "variável de ambiente"
	Conta   *services.MPAccount
	Erro    string
	OK      bool
}

// testMercadoPago confere o Access Token em uso, sem nunca exibi-lo.
func testMercadoPago() mpTestResult {
	res := mpTestResult{Origem: "painel"}
	if os.Getenv("MERCADOPAGO_ACCESS_TOKEN") != "" {
		res.Origem = "variável de ambiente MERCADOPAGO_ACCESS_TOKEN"
	}
	token := models.GetSetting("mp_access_token")
	res.Formato = services.ClassifyMPToken(token)
	if res.Formato.Tipo == "vazio" {
		return res
	}
	conta, err := services.MPWhoAmI(token)
	if err != nil {
		res.Erro = err.Error()
		return res
	}
	res.Conta, res.OK = conta, true
	return res
}

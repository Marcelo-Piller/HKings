package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vendas/loja/models"
	"github.com/vendas/loja/services"
)

// Frete com o Melhor Envio: cotação no checkout, endereço de despacho, conexão
// da conta e geração da etiqueta. O vendedor (e o admin, para os livros do
// próprio site, seller 0) usa a própria conta Melhor Envio.

const (
	meStateKey = "me_oauth_state"
	meOwnerKey = "me_oauth_owner" // id do vendedor, ou -1 para o admin (site)
)

// quoteLimiter: a cotação chama uma API externa a cada CEP digitado.
var quoteLimiter = newRateLimiter(60, 10*time.Minute, 10*time.Minute)

func meConfig() services.MEConfig {
	contato := strings.TrimSpace(models.GetSetting("melhorenvio_contato"))
	if contato == "" {
		contato = strings.TrimSpace(models.GetSetting("smtp_user"))
	}
	return services.MEConfig{
		ClientID:     models.GetSetting("melhorenvio_client_id"),
		ClientSecret: models.GetSetting("melhorenvio_client_secret"),
		Sandbox:      models.GetSetting("melhorenvio_sandbox") == "1",
		Contact:      contato,
	}
}

// meRedirectURI é a volta da autorização; precisa ser idêntica à cadastrada
// no aplicativo do Melhor Envio.
func meRedirectURI() string {
	base := strings.TrimRight(models.GetSetting("store_base_url"), "/")
	if base == "" {
		return ""
	}
	return base + "/melhorenvio/retorno"
}

// meAvailable diz se a loja já configurou o aplicativo do Melhor Envio.
func meAvailable() bool {
	cfg := meConfig()
	return cfg.ClientID != "" && cfg.ClientSecret != "" && meRedirectURI() != ""
}

// meAPIFor devolve a API autenticada com a conta do vendedor (0 = site),
// renovando o token quando faltam menos de 5 dias para vencer.
func meAPIFor(sellerID int) (*services.MEAPI, error) {
	acc, err := models.GetMEAccount(sellerID)
	if err != nil {
		return nil, err
	}
	cfg := meConfig()
	if !acc.ExpiresAt.IsZero() && time.Until(acc.ExpiresAt) < 5*24*time.Hour && acc.RefreshToken != "" {
		if novos, err := services.MERefresh(cfg, acc.RefreshToken); err == nil {
			if err := models.SaveMETokens(sellerID, novos.AccessToken, novos.RefreshToken, novos.ExpiresAt); err != nil {
				log.Printf("melhor envio: vendedor %d: token renovado, mas não gravado: %v", sellerID, err)
			}
			acc.AccessToken = novos.AccessToken
		} else {
			log.Printf("melhor envio: vendedor %d: renovação do token falhou: %v", sellerID, err)
		}
	}
	return &services.MEAPI{Config: cfg, Token: acc.AccessToken}, nil
}

// ── Cotação ──────────────────────────────────────────────────────────────────

// errNoQuote: não há como cotar (vendedor sem endereço de despacho ou nenhuma
// conta Melhor Envio conectada). O pedido segue com "frete combinado".
var errNoQuote = errors.New("cotação de frete indisponível")

// packageItem descreve um livro para a cotação: medidas em cm, peso em kg.
func packageItem(p models.Product, qty int) services.MEPackageItem {
	return services.MEPackageItem{
		ID:             strconv.Itoa(p.ID),
		Width:          float64(p.PackageWidth()),
		Height:         float64(p.PackageHeight()),
		Length:         float64(p.PackageLength()),
		Weight:         float64(p.PackageWeightG()) / 1000,
		InsuranceValue: p.Price,
		Quantity:       qty,
	}
}

// quoteFrom cota o frete de quem despacha (sellerID) até o CEP. Usa a conta
// Melhor Envio do vendedor; sem ela, a do site, só para estimar o preço.
func quoteFrom(sellerID int, items []services.MEPackageItem, cep string) ([]services.MEQuote, error) {
	if len(items) == 0 {
		return nil, errNoQuote
	}
	origin := models.GetShippingOrigin(sellerID)
	if !origin.Complete() {
		return nil, errNoQuote
	}
	api, err := meAPIFor(sellerID)
	if err != nil {
		if api, err = meAPIFor(0); err != nil {
			return nil, errNoQuote
		}
	}
	return api.Calculate(origin.PostalCode, cep, items)
}

// quoteForCart cota o frete dos livros físicos da sacola até o CEP.
func quoteForCart(cart *models.Cart, cep string) ([]services.MEQuote, error) {
	sellerID, _, ok := cart.Seller()
	if !ok || !cart.HasPhysical() {
		return nil, errNoQuote
	}
	var items []services.MEPackageItem
	for _, it := range cart.Items {
		if !it.Product.IsDigital() {
			items = append(items, packageItem(it.Product, it.Quantity))
		}
	}
	return quoteFrom(sellerID, items, cep)
}

// quoteForProduct cota o frete de um exemplar, para a página do livro.
func quoteForProduct(productID int, cep string) ([]services.MEQuote, error) {
	p, err := models.GetProduct(productID)
	if err != nil || !p.Active || p.IsDigital() {
		return nil, errNoQuote
	}
	return quoteFrom(p.SellerID, []services.MEPackageItem{packageItem(*p, 1)}, cep)
}

type quoteOption struct {
	ID    int    `json:"id"`
	Nome  string `json:"nome"`
	Preco string `json:"preco"`
	Prazo int    `json:"prazo"`
}

// ShippingQuote - GET /frete/cotar?cep=: endereço do CEP e opções de frete.
// Com "livro=ID", cota um exemplar daquele livro (página do produto); sem ele,
// cota a sacola inteira (checkout). Com "so_endereco=1", só busca o endereço.
func ShippingQuote(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	responder := func(status int, v any) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	if allowed, _ := quoteLimiter.Allowed("cotar|" + clientIP(r)); !allowed {
		responder(http.StatusTooManyRequests, map[string]string{"erro": "Muitas consultas de frete. Aguarde alguns minutos."})
		return
	}
	quoteLimiter.RegisterFailure("cotar|" + clientIP(r))

	cep := models.NormalizeCEP(r.URL.Query().Get("cep"))
	if cep == "" {
		responder(http.StatusBadRequest, map[string]string{"erro": "Informe um CEP com 8 dígitos."})
		return
	}
	out := map[string]any{"cep": models.FormatCEP(cep)}
	if end, err := services.LookupCEP(cep); err == nil {
		out["endereco"] = end
	} else {
		out["aviso"] = "Não encontramos esse CEP. Confira o número ou preencha o endereço à mão."
	}
	// Painéis de envio só querem o endereço do CEP, sem cotar a sacola.
	if r.URL.Query().Get("so_endereco") == "1" {
		responder(http.StatusOK, out)
		return
	}

	var quotes []services.MEQuote
	var err error
	if livro, _ := strconv.Atoi(r.URL.Query().Get("livro")); livro > 0 {
		quotes, err = quoteForProduct(livro, cep)
	} else {
		quotes, err = quoteForCart(getCart(r), cep)
	}
	switch {
	case errors.Is(err, errNoQuote):
		out["combinado"] = true
	case err != nil:
		log.Printf("cotação de frete para %s: %v", cep, err)
		out["erro"] = "Não foi possível calcular o frete agora. Tente de novo em instantes."
	default:
		opcoes := make([]quoteOption, 0, len(quotes))
		for _, q := range quotes {
			opcoes = append(opcoes, quoteOption{ID: q.ServiceID, Nome: q.Label(), Preco: models.FormatBRL(float64(q.PriceCents) / 100), Prazo: q.Days})
		}
		out["opcoes"] = opcoes
		if len(opcoes) == 0 {
			out["erro"] = "Nenhuma modalidade de frete atende esse CEP."
		}
	}
	responder(http.StatusOK, out)
}

// shippingFromForm confere, no fechamento do pedido, o frete escolhido. O
// preço vem de uma cotação nova feita aqui, nunca do formulário.
func shippingFromForm(r *http.Request, cart *models.Cart, cep string) (models.Shipping, error) {
	quotes, err := quoteForCart(cart, cep)
	if errors.Is(err, errNoQuote) {
		return models.Shipping{Service: "Frete combinado com o vendedor"}, nil
	}
	if err != nil {
		return models.Shipping{}, err
	}
	escolhido, _ := strconv.Atoi(r.FormValue("frete"))
	for _, q := range quotes {
		if q.ServiceID == escolhido {
			return models.Shipping{Cents: q.PriceCents, ServiceID: q.ServiceID, Service: q.Label(), Days: q.Days}, nil
		}
	}
	return models.Shipping{}, errFreteNaoEscolhido
}

var errFreteNaoEscolhido = errors.New("escolha uma opção de frete")

// ── Endereço de despacho e conta (vendedor e admin) ───────────────────────────

var envioAvisos = map[string]struct {
	texto string
	ok    bool
}{
	"salvo":        {"Endereço de envio salvo.", true},
	"conectado":    {"Conta Melhor Envio conectada. Você já pode gerar etiquetas nos pedidos.", true},
	"desconectado": {"Conta Melhor Envio desconectada.", true},
	"negado":       {"Você cancelou a autorização no Melhor Envio. Nada foi alterado.", false},
	"erro":         {"Não foi possível conectar a conta Melhor Envio. Tente de novo.", false},
	"indisponivel": {"A integração com o Melhor Envio ainda não foi configurada pela loja.", false},
}

func shippingPageData(r *http.Request, ownerID int) map[string]any {
	data := map[string]any{
		"Origem":       models.GetShippingOrigin(ownerID),
		"MEDisponivel": meAvailable(),
		"Sandbox":      meConfig().Sandbox,
	}
	if aviso, ok := envioAvisos[r.URL.Query().Get("envio")]; ok {
		data["Aviso"], data["AvisoOK"] = aviso.texto, aviso.ok
	}
	return data
}

func originFromForm(r *http.Request) models.Address {
	return models.Address{
		PostalCode: r.FormValue("cep"), Street: r.FormValue("rua"), Number: r.FormValue("numero"),
		Complement: r.FormValue("complemento"), District: r.FormValue("bairro"),
		City: r.FormValue("cidade"), State: r.FormValue("uf"),
	}
}

// SellerShipping - GET/POST /vendedor/envio
func SellerShipping(w http.ResponseWriter, r *http.Request) {
	seller := currentSeller(r)
	if r.Method == http.MethodPost {
		r.ParseForm()
		if err := models.SaveShippingOrigin(seller.ID, originFromForm(r), r.FormValue("telefone")); err != nil {
			data := shippingPageData(r, seller.ID)
			data["Seller"] = seller
			data["Error"] = "Preencha CEP, rua, número, bairro, cidade e UF."
			renderSeller(w, r, "seller_shipping.html", data)
			return
		}
		http.Redirect(w, r, "/vendedor/envio?envio=salvo", http.StatusSeeOther)
		return
	}
	data := shippingPageData(r, seller.ID)
	data["Seller"] = seller
	renderSeller(w, r, "seller_shipping.html", data)
}

// AdminShipping - GET/POST /admin/envio: de onde o site despacha os livros
// que vende diretamente.
func AdminShipping(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		if err := models.SaveShippingOrigin(0, originFromForm(r), r.FormValue("telefone")); err != nil {
			data := shippingPageData(r, 0)
			data["Error"] = "Preencha CEP, rua, número, bairro, cidade e UF."
			renderAdmin(w, r, "admin_shipping.html", data)
			return
		}
		http.Redirect(w, r, "/admin/envio?envio=salvo", http.StatusSeeOther)
		return
	}
	renderAdmin(w, r, "admin_shipping.html", shippingPageData(r, 0))
}

// meOwner identifica quem está conectando: o vendedor logado ou o admin (site).
func meOwner(r *http.Request, paraAdmin bool) (owner int, voltar string, ok bool) {
	if paraAdmin {
		return -1, "/admin/envio", isAdminLoggedIn(r)
	}
	if s := currentSeller(r); s != nil {
		return s.ID, "/vendedor/envio", true
	}
	return 0, "/vendedor/login", false
}

// MEConnect - GET /melhorenvio/conectar[?para=admin]
func MEConnect(w http.ResponseWriter, r *http.Request) {
	owner, voltar, ok := meOwner(r, r.URL.Query().Get("para") == "admin")
	if !ok {
		http.Redirect(w, r, voltar, http.StatusSeeOther)
		return
	}
	if !meAvailable() {
		http.Redirect(w, r, voltar+"?envio=indisponivel", http.StatusSeeOther)
		return
	}
	state, err := randomToken()
	if err != nil {
		http.Error(w, "não foi possível iniciar a conexão", http.StatusInternalServerError)
		return
	}
	sess, _ := store.Get(r, "session")
	sess.Values[meStateKey] = state
	sess.Values[meOwnerKey] = owner
	if err := sess.Save(r, w); err != nil {
		http.Error(w, "não foi possível salvar a sessão", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, services.MEAuthorizationURL(meConfig(), meRedirectURI(), state), http.StatusSeeOther)
}

// MECallback - GET /melhorenvio/retorno
func MECallback(w http.ResponseWriter, r *http.Request) {
	sess, _ := store.Get(r, "session")
	expected, _ := sess.Values[meStateKey].(string)
	owner, temDono := sess.Values[meOwnerKey].(int)
	delete(sess.Values, meStateKey)
	delete(sess.Values, meOwnerKey)
	_ = sess.Save(r, w)

	// O dono gravado na ida precisa ser quem está logado na volta.
	ownerID, voltar, ok := meOwner(r, owner == -1)
	if !temDono || !ok || (owner != -1 && owner != ownerID) {
		http.Redirect(w, r, voltar, http.StatusSeeOther)
		return
	}
	if owner == -1 {
		ownerID = 0
	}
	q := r.URL.Query()
	state := q.Get("state")
	if expected == "" || len(state) != len(expected) || subtle.ConstantTimeCompare([]byte(state), []byte(expected)) != 1 {
		http.Redirect(w, r, voltar+"?envio=erro", http.StatusSeeOther)
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		http.Redirect(w, r, voltar+"?envio=negado", http.StatusSeeOther)
		return
	}
	tokens, err := services.MEExchangeCode(meConfig(), q.Get("code"), meRedirectURI())
	if err != nil {
		log.Printf("melhor envio: conexão do vendedor %d: %v", ownerID, err)
		http.Redirect(w, r, voltar+"?envio=erro", http.StatusSeeOther)
		return
	}
	if err := models.SaveMETokens(ownerID, tokens.AccessToken, tokens.RefreshToken, tokens.ExpiresAt); err != nil {
		log.Printf("melhor envio: gravação dos tokens do vendedor %d: %v", ownerID, err)
		http.Redirect(w, r, voltar+"?envio=erro", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, voltar+"?envio=conectado", http.StatusSeeOther)
}

// MEDisconnect - POST /melhorenvio/desconectar[?para=admin]
func MEDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	owner, voltar, ok := meOwner(r, r.FormValue("para") == "admin")
	if !ok {
		http.Redirect(w, r, voltar, http.StatusSeeOther)
		return
	}
	if owner == -1 {
		owner = 0
	}
	if err := models.DisconnectME(owner); err != nil {
		log.Printf("melhor envio: desconexão do vendedor %d: %v", owner, err)
	}
	http.Redirect(w, r, voltar+"?envio=desconectado", http.StatusSeeOther)
}

// ── Etiqueta ─────────────────────────────────────────────────────────────────

// generateLabel gera a etiqueta do pedido na conta Melhor Envio de quem vende.
func generateLabel(order *models.Order, sellerID int, invoiceKey string) error {
	if !order.CanGenerateLabel() {
		return errors.New("este pedido não está pronto para etiqueta (precisa estar pago, com frete do Melhor Envio e sem etiqueta)")
	}
	origin := models.GetShippingOrigin(sellerID)
	if !origin.Complete() {
		return errors.New("cadastre o endereço de envio antes de gerar etiquetas")
	}
	api, err := meAPIFor(sellerID)
	if err != nil {
		return errors.New("conecte sua conta Melhor Envio antes de gerar etiquetas")
	}

	from := services.MEAddress{
		Phone: origin.Phone, Address: origin.Street, Number: origin.Number, Complement: origin.Complement,
		District: origin.District, City: origin.City, StateAbbr: origin.State, CountryID: "BR", PostalCode: origin.PostalCode,
	}
	if sellerID == 0 {
		from.Name = models.GetSetting("store_name")
		if from.Name == "" {
			from.Name = "Livredo"
		}
		from.Email = models.GetSetting("smtp_user")
	} else if seller, err := models.GetSeller(sellerID); err == nil {
		from.Name, from.Email = seller.Name, seller.Email
		if len(seller.Document) == 14 {
			from.CompanyDocument = seller.Document
		} else {
			from.Document = seller.Document
		}
	}
	to := services.MEAddress{
		Name: order.CustomerName, Phone: order.CustomerPhone, Email: order.CustomerEmail,
		Address: order.ShipTo.Street, Number: order.ShipTo.Number, Complement: order.ShipTo.Complement,
		District: order.ShipTo.District, City: order.ShipTo.City, StateAbbr: order.ShipTo.State,
		CountryID: "BR", PostalCode: order.ShipTo.PostalCode,
	}
	if len(order.CustomerDocument) == 14 {
		to.CompanyDocument = order.CustomerDocument
	} else {
		to.Document = order.CustomerDocument
	}

	// Um volume só: o maior lado de cada medida, alturas somadas, pesos somados.
	var vol services.MEVolume
	var produtos []services.MELabelProduct
	var valor float64
	for _, it := range order.Items {
		if it.Digital {
			continue
		}
		p, err := models.GetProduct(it.ProductID)
		if err != nil {
			p = &models.Product{} // produto apagado: pacote padrão
		}
		vol.Height += float64(p.PackageHeight() * it.Quantity)
		vol.Width = max(vol.Width, float64(p.PackageWidth()))
		vol.Length = max(vol.Length, float64(p.PackageLength()))
		vol.Weight += float64(p.PackageWeightG()*it.Quantity) / 1000
		produtos = append(produtos, services.MELabelProduct{Name: it.ProductName, Quantity: it.Quantity, UnitaryValue: it.Price})
		valor += it.Price * float64(it.Quantity)
	}

	label, err := api.GenerateLabel(services.MELabelRequest{
		ServiceID: order.Shipping.ServiceID, From: from, To: to, Products: produtos, Volume: vol,
		InsuranceValue: valor, InvoiceKey: invoiceKey, Tag: fmt.Sprintf("Livredo #%d", order.ID),
	})
	if label != nil && label.OrderID != "" {
		// Grava mesmo com erro na compra: a etiqueta fica no carrinho do
		// Melhor Envio, e o vendedor resolve por lá (ex.: falta de saldo).
		if serr := models.SetOrderLabel(order.ID, label.OrderID, label.PrintURL, label.Tracking); serr != nil {
			log.Printf("etiqueta do pedido #%d: gravação: %v", order.ID, serr)
		}
	}
	return err
}

// labelErrorMessage traduz o erro da etiqueta para quem está no painel.
func labelErrorMessage(err error) string {
	msg := err.Error()
	switch {
	case errors.Is(err, services.ErrMEUnauthorized):
		return "Sua conta Melhor Envio está desconectada. Conecte de novo em Envio."
	case strings.Contains(strings.ToLower(msg), "saldo"):
		return "Saldo insuficiente no Melhor Envio. Adicione saldo na sua conta de lá; a etiqueta ficou no seu carrinho do Melhor Envio."
	case strings.HasPrefix(msg, "melhor envio:") || strings.Contains(msg, ": melhor envio:"):
		return "O Melhor Envio recusou a etiqueta: " + msg
	default:
		return capitalizeFirst(msg) + "."
	}
}

// handleLabelAction trata os botões de etiqueta e rastreio da página do
// pedido, iguais para vendedor e admin. Devolve a mensagem de erro, se houver.
func handleLabelAction(r *http.Request, order *models.Order, sellerID int) string {
	switch r.FormValue("acao") {
	case "etiqueta":
		chave := strings.Join(strings.Fields(r.FormValue("nfe")), "")
		if chave != "" && len(chave) != 44 {
			return "A chave da NF-e tem 44 dígitos. Deixe em branco para enviar com declaração de conteúdo."
		}
		if err := generateLabel(order, sellerID, chave); err != nil {
			log.Printf("etiqueta do pedido #%d: %v", order.ID, err)
			return labelErrorMessage(err)
		}
	case "rastreio":
		if err := models.SetOrderTracking(order.ID, r.FormValue("codigo")); err != nil {
			return capitalizeFirst(err.Error()) + "."
		}
	case "atualizar_rastreio":
		if order.MEOrderID == "" {
			return "Este pedido não tem etiqueta do Melhor Envio."
		}
		api, err := meAPIFor(sellerID)
		if err != nil {
			return "Conecte sua conta Melhor Envio em Envio."
		}
		if url, err := api.PrintURL(order.MEOrderID); err == nil && url != "" && order.LabelURL == "" {
			models.SetOrderLabel(order.ID, order.MEOrderID, url, "")
		}
		code, err := api.Tracking(order.MEOrderID)
		if err != nil {
			return labelErrorMessage(err)
		}
		if code == "" {
			return "O código de rastreio ainda não saiu. Ele costuma aparecer depois da postagem."
		}
		models.SetOrderTracking(order.ID, code)
	}
	return ""
}

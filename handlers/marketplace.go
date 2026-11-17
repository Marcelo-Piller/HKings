package handlers

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vendas/loja/models"
	"github.com/vendas/loja/services"
)

// Split de pagamentos.
//
// Livro do próprio site: cobrança com o token do site, 100% na conta do site.
// Livro de vendedor: cobrança com o token do vendedor (obtido pelo OAuth), na
// conta dele, com a comissão do site retida pelo Mercado Pago.

const oauthStateKey = "mp_oauth_state"

// renovarAntes: o token do vendedor vale 180 dias; renovamos com folga para
// uma venda nunca pegar um token vencido.
const renovarAntes = 15 * 24 * time.Hour

var errSellerNotReady = errors.New("vendedor não pode receber agora")

// ── Conta do próprio site ───────────────────────────────────────────────────

var siteCollector struct {
	sync.Mutex
	token string
	id    int64
}

// siteCollectorID descobre (e guarda em memória) qual conta Mercado Pago é a
// do site, para conferir que venda do site foi recebida nela.
func siteCollectorID() (int64, error) {
	token := models.GetSetting("mp_access_token")
	if token == "" {
		return 0, fmt.Errorf("access token do site não configurado")
	}
	siteCollector.Lock()
	defer siteCollector.Unlock()
	if siteCollector.token == token && siteCollector.id != 0 {
		return siteCollector.id, nil
	}
	id, err := services.GetUserID(token)
	if err != nil {
		return 0, err
	}
	siteCollector.token, siteCollector.id = token, id
	return id, nil
}

// ── Tokens ──────────────────────────────────────────────────────────────────

// sellerAccessToken devolve o token do vendedor, renovando-o se estiver perto
// de vencer. Se a renovação falhar mas o token ainda valer, segue com ele.
func sellerAccessToken(sellerID int) (token, userID string, err error) {
	account, err := models.GetSellerMPAccount(sellerID)
	if err != nil {
		return "", "", err
	}
	precisaRenovar := !account.ExpiresAt.IsZero() && time.Until(account.ExpiresAt) < renovarAntes
	if precisaRenovar && account.RefreshToken != "" {
		novos, rerr := services.RefreshSellerToken(
			models.GetSetting("mp_client_id"), models.GetSetting("mp_client_secret"), account.RefreshToken)
		if rerr == nil {
			if serr := models.SaveSellerMPTokens(sellerID, novos.UserID, novos.AccessToken, novos.RefreshToken, novos.ExpiresAt); serr != nil {
				log.Printf("vendedor %d: token renovado, mas não gravado: %v", sellerID, serr)
			}
			return novos.AccessToken, novos.UserID, nil
		}
		log.Printf("vendedor %d: renovação do token do Mercado Pago falhou: %v", sellerID, rerr)
	}
	if !account.ExpiresAt.IsZero() && time.Now().After(account.ExpiresAt) {
		return "", "", fmt.Errorf("%w: token do Mercado Pago do vendedor venceu", errSellerNotReady)
	}
	return account.AccessToken, account.UserID, nil
}

// paymentAccountForOrder escolhe com qual conta cobrar um pedido: o token a
// usar, a conta que deve receber e a comissão do site.
func paymentAccountForOrder(order *models.Order) (token, collector string, fee float64, err error) {
	if order.SellerID == 0 {
		token = models.GetSetting("mp_access_token")
		if token == "" {
			return "", "", 0, fmt.Errorf("access token do site não configurado")
		}
		if id, cerr := siteCollectorID(); cerr == nil {
			collector = strconv.FormatInt(id, 10)
		} else {
			log.Printf("não foi possível identificar a conta do site: %v", cerr)
		}
		return token, collector, 0, nil
	}
	token, collector, err = sellerAccessToken(order.SellerID)
	if err != nil {
		return "", "", 0, err
	}
	return token, collector, order.MarketplaceFee(), nil
}

// tokenForCollector escolhe o token para consultar um pagamento avisado pelo
// webhook. O aviso traz a conta que recebeu (user_id): se for de um vendedor,
// só o token dele enxerga o pagamento.
func tokenForCollector(userID string) string {
	if userID != "" {
		if sellerID, err := models.SellerIDByMPUser(userID); err == nil {
			if token, _, err := sellerAccessToken(sellerID); err == nil {
				return token
			}
		}
	}
	return models.GetSetting("mp_access_token")
}

// tokenForOrder escolhe o token para consultar um pagamento de um pedido
// conhecido (na volta do Checkout Pro).
func tokenForOrder(order *models.Order) string {
	if order.SellerID != 0 {
		if token, _, err := sellerAccessToken(order.SellerID); err == nil {
			return token
		}
	}
	return models.GetSetting("mp_access_token")
}

// ── Conferência ─────────────────────────────────────────────────────────────

var (
	errWrongCollector = errors.New("pagamento recebido em outra conta")
	errFeeMissing     = errors.New("comissão do site ausente ou menor que a combinada")
)

// checkPaymentMatchesOrder confere que o dinheiro foi para quem deveria e que
// a comissão do site foi retida. Sem isso, um vendedor poderia criar na
// própria conta um pagamento apontando para um pedido de outro (ou do site) e
// marcá-lo como pago sem que o dinheiro chegasse a quem vendeu.
func checkPaymentMatchesOrder(payment *services.PaymentDetails, order *models.Order, expectedCollector int64) error {
	if expectedCollector == 0 || payment.CollectorID != expectedCollector {
		return fmt.Errorf("%w: esperado %d, recebido em %d", errWrongCollector, expectedCollector, payment.CollectorID)
	}
	// Tolerância de 1 centavo para arredondamento do lado do Mercado Pago.
	if order.MarketplaceFeeCents > 0 && payment.ApplicationFeeCents+1 < order.MarketplaceFeeCents {
		return fmt.Errorf("%w: combinada %d centavos, retida %d", errFeeMissing, order.MarketplaceFeeCents, payment.ApplicationFeeCents)
	}
	return nil
}

// expectedCollectorFor devolve a conta que deve ter recebido o pedido.
func expectedCollectorFor(order *models.Order) (int64, error) {
	if order.MPCollectorID != "" {
		return strconv.ParseInt(order.MPCollectorID, 10, 64)
	}
	// Pedidos anteriores ao split não gravavam o recebedor: eram todos do site.
	if order.SellerID == 0 {
		return siteCollectorID()
	}
	return 0, fmt.Errorf("pedido #%d sem recebedor registrado", order.ID)
}

// ── Conexão do vendedor (OAuth) ─────────────────────────────────────────────

// mpRedirectURI é o endereço de volta do OAuth. Precisa ser idêntico ao
// cadastrado no aplicativo do Mercado Pago.
func mpRedirectURI() string {
	base := strings.TrimRight(models.GetSetting("store_base_url"), "/")
	if base == "" {
		return ""
	}
	return base + "/vendedor/mercadopago/retorno"
}

// SellerMPConnect manda o vendedor ao Mercado Pago para autorizar o site.
func SellerMPConnect(w http.ResponseWriter, r *http.Request) {
	clientID := models.GetSetting("mp_client_id")
	redirect := mpRedirectURI()
	if clientID == "" || models.GetSetting("mp_client_secret") == "" || redirect == "" {
		log.Print("conexão Mercado Pago: faltam Client ID, Client Secret ou URL base da loja")
		http.Redirect(w, r, "/vendedor?mp=indisponivel", http.StatusSeeOther)
		return
	}

	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		http.Error(w, "não foi possível iniciar a conexão", http.StatusInternalServerError)
		return
	}
	state := base64.RawURLEncoding.EncodeToString(raw[:])
	sess, _ := store.Get(r, "session")
	sess.Values[oauthStateKey] = state
	if err := sess.Save(r, w); err != nil {
		http.Error(w, "não foi possível salvar a sessão", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, services.AuthorizationURL(clientID, redirect, state), http.StatusSeeOther)
}

// SellerMPCallback recebe o vendedor de volta do Mercado Pago.
func SellerMPCallback(w http.ResponseWriter, r *http.Request) {
	seller := currentSeller(r)
	sess, _ := store.Get(r, "session")
	expected, _ := sess.Values[oauthStateKey].(string)
	delete(sess.Values, oauthStateKey)
	_ = sess.Save(r, w)

	q := r.URL.Query()
	// state confere que a volta é deste vendedor, nesta sessão. Sem isso, um
	// atacante poderia fazer o vendedor "conectar" a conta do atacante e
	// desviar para ela o dinheiro das vendas.
	state := q.Get("state")
	if expected == "" || len(state) != len(expected) || subtle.ConstantTimeCompare([]byte(state), []byte(expected)) != 1 {
		log.Printf("vendedor %d: volta do Mercado Pago com state inválido (sessão sem state: %v)", sellerLogID(seller), expected == "")
		http.Redirect(w, r, "/vendedor?mp=erro", http.StatusSeeOther)
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		log.Printf("vendedor %d: Mercado Pago devolveu sem código: error=%q %q", sellerLogID(seller), q.Get("error"), q.Get("error_description"))
		http.Redirect(w, r, "/vendedor?mp=negado", http.StatusSeeOther)
		return
	}

	tokens, err := services.ExchangeAuthorizationCode(
		models.GetSetting("mp_client_id"), models.GetSetting("mp_client_secret"), q.Get("code"), mpRedirectURI())
	if err != nil {
		log.Printf("vendedor %d: troca do código OAuth: %v", seller.ID, err)
		http.Redirect(w, r, "/vendedor?mp=erro", http.StatusSeeOther)
		return
	}

	// A conta do próprio site não pode ser a de um vendedor: a comissão iria
	// para ela mesma e o Mercado Pago recusaria as cobranças.
	if siteID, err := siteCollectorID(); err == nil && strconv.FormatInt(siteID, 10) == tokens.UserID {
		http.Redirect(w, r, "/vendedor?mp=conta-do-site", http.StatusSeeOther)
		return
	}

	if err := models.SaveSellerMPTokens(seller.ID, tokens.UserID, tokens.AccessToken, tokens.RefreshToken, tokens.ExpiresAt); err != nil {
		log.Printf("vendedor %d: gravação dos tokens: %v", seller.ID, err)
		http.Redirect(w, r, "/vendedor?mp=erro", http.StatusSeeOther)
		return
	}
	log.Printf("vendedor %d conectou a conta Mercado Pago %s", seller.ID, tokens.UserID)
	http.Redirect(w, r, "/vendedor?mp=conectado", http.StatusSeeOther)
}

// SellerMPDisconnect desliga a conta do vendedor.
func SellerMPDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	seller := currentSeller(r)
	if err := models.DisconnectSellerMP(seller.ID); err != nil {
		log.Printf("vendedor %d: desconexão: %v", seller.ID, err)
	}
	http.Redirect(w, r, "/vendedor?mp=desconectado", http.StatusSeeOther)
}

// sellerLogID devolve o ID do vendedor para o log, ou 0 sem vendedor.
func sellerLogID(s *models.Seller) int {
	if s == nil {
		return 0
	}
	return s.ID
}

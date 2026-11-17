package handlers

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/vendas/loja/models"
	"github.com/vendas/loja/services"
)

// Login de clientes com Google. Vale só para compradores: vendedores e admin
// continuam com usuário e senha próprios.

const (
	googleStateKey    = "google_state"
	googleNonceKey    = "google_nonce"
	googleVerifierKey = "google_verifier"
	googleVoltaKey    = "google_volta"
	googlePapelKey    = "google_papel"

	// Papéis do login com Google. O endereço de volta é um só (o cadastrado
	// no Google Cloud), então a sessão guarda para que serve cada ida.
	papelVendedor         = "vendedor"          // entrar no painel do vendedor
	papelVincularVendedor = "vincular_vendedor" // ligar o Google ao vendedor logado
)

// googleRedirectURI precisa ser idêntico ao cadastrado no Google Cloud.
func googleRedirectURI() string {
	base := strings.TrimRight(models.GetSetting("store_base_url"), "/")
	if base == "" {
		return ""
	}
	return base + "/conta/google/retorno"
}

// googleLoginAvailable diz se o botão "Continuar com o Google" deve aparecer.
func googleLoginAvailable() bool {
	return models.GetSetting("google_client_id") != "" &&
		models.GetSetting("google_client_secret") != "" && googleRedirectURI() != ""
}

var googleAvisos = map[string]string{
	"indisponivel":   "O login com Google ainda não está disponível nesta loja.",
	"cancelado":      "Login com Google cancelado. Nada foi alterado.",
	"erro":           "Não foi possível entrar com o Google agora. Tente de novo.",
	"nao-verificado": "Sua conta Google não tem o e-mail verificado. Verifique-o no Google ou crie uma conta com senha.",
	"conflito":       "Este e-mail já está ligado a outra conta Google. Entre com a conta Google usada antes.",
}

func randomToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// CustomerGoogleStart - GET /conta/google: manda o cliente ao Google.
func CustomerGoogleStart(w http.ResponseWriter, r *http.Request) {
	papel := r.URL.Query().Get("papel")
	switch papel {
	case papelVendedor:
		if currentSeller(r) != nil {
			http.Redirect(w, r, "/vendedor", http.StatusSeeOther)
			return
		}
	case papelVincularVendedor:
		if currentSeller(r) == nil {
			http.Redirect(w, r, "/vendedor/login", http.StatusSeeOther)
			return
		}
	default:
		papel = ""
		if isCustomerLoggedIn(r) {
			http.Redirect(w, r, "/conta", http.StatusSeeOther)
			return
		}
	}
	if !googleLoginAvailable() {
		http.Redirect(w, r, googleFailURL(papel, "indisponivel"), http.StatusSeeOther)
		return
	}
	state, err1 := randomToken()
	nonce, err2 := randomToken()
	verifier, err3 := randomToken()
	if err1 != nil || err2 != nil || err3 != nil {
		http.Error(w, "não foi possível iniciar o login", http.StatusInternalServerError)
		return
	}
	sess, _ := store.Get(r, "session")
	sess.Values[googleStateKey] = state
	sess.Values[googleNonceKey] = nonce
	sess.Values[googleVerifierKey] = verifier
	sess.Values[googleVoltaKey] = safeReturnPath(r.URL.Query().Get("volta"))
	sess.Values[googlePapelKey] = papel
	if err := sess.Save(r, w); err != nil {
		http.Error(w, "não foi possível salvar a sessão", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, services.GoogleAuthorizationURL(
		models.GetSetting("google_client_id"), googleRedirectURI(), state, nonce, verifier), http.StatusSeeOther)
}

// CustomerGoogleCallback - GET /conta/google/retorno: volta do Google.
func CustomerGoogleCallback(w http.ResponseWriter, r *http.Request) {
	sess, _ := store.Get(r, "session")
	expected, _ := sess.Values[googleStateKey].(string)
	nonce, _ := sess.Values[googleNonceKey].(string)
	verifier, _ := sess.Values[googleVerifierKey].(string)
	volta, _ := sess.Values[googleVoltaKey].(string)
	papel, _ := sess.Values[googlePapelKey].(string)
	// Cada início de login vale para uma única volta.
	delete(sess.Values, googleStateKey)
	delete(sess.Values, googleNonceKey)
	delete(sess.Values, googleVerifierKey)
	delete(sess.Values, googleVoltaKey)
	delete(sess.Values, googlePapelKey)
	_ = sess.Save(r, w)

	falha := func(code string) {
		http.Redirect(w, r, googleFailURL(papel, code), http.StatusSeeOther)
	}

	q := r.URL.Query()
	// state confere que a volta pertence a quem começou o login neste
	// navegador. Sem isso, um atacante poderia logar a vítima na conta dele
	// e ver o endereço de entrega que ela digitasse depois.
	state := q.Get("state")
	if expected == "" || len(state) != len(expected) || subtle.ConstantTimeCompare([]byte(state), []byte(expected)) != 1 {
		falha("erro")
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		falha("cancelado")
		return
	}

	identity, err := services.ExchangeGoogleCode(
		models.GetSetting("google_client_id"), models.GetSetting("google_client_secret"),
		q.Get("code"), googleRedirectURI(), verifier, nonce)
	if err != nil {
		log.Printf("login com Google: %v", err)
		falha("erro")
		return
	}

	if papel != "" {
		sellerGoogleReturn(w, r, papel, identity.Subject)
		return
	}

	result, err := models.LoginWithGoogle(identity.Subject, identity.Email, identity.Name, identity.EmailVerified)
	switch {
	case errors.Is(err, models.ErrGoogleEmailUnverified):
		falha("nao-verificado")
		return
	case errors.Is(err, models.ErrGoogleAccountConflict):
		falha("conflito")
		return
	case err != nil:
		log.Printf("login com Google: %v", err)
		falha("erro")
		return
	}

	if err := rotateCSRFToken(sess); err != nil {
		http.Error(w, "não foi possível iniciar a sessão", http.StatusInternalServerError)
		return
	}
	sess.Values["customer_id"] = result.Customer.ID
	if err := sess.Save(r, w); err != nil {
		http.Error(w, "não foi possível salvar a sessão", http.StatusInternalServerError)
		return
	}
	destino := "/"
	switch {
	case safeReturnPath(volta) != "":
		destino = safeReturnPath(volta)
	case result.Linked:
		destino = "/conta?google=vinculada"
	case result.Created:
		sendWelcomeEmail(result.Customer.Email, welcomeData{Nome: result.Customer.Name})
		destino = "/conta?google=nova"
	}
	// Conta criada pelo Google ainda não aceitou os termos: aceita agora.
	http.Redirect(w, r, consentGate(models.SubjectCustomer, result.Customer.ID, destino), http.StatusSeeOther)
}

// googleFailURL é para onde volta um login com Google que não deu certo.
func googleFailURL(papel, code string) string {
	switch papel {
	case papelVendedor:
		return "/vendedor/login?google=" + code
	case papelVincularVendedor:
		return "/vendedor?google=" + code
	}
	return "/conta/entrar?google=" + code
}

// sellerGoogleReturn conclui o login ou o vínculo do vendedor com Google.
func sellerGoogleReturn(w http.ResponseWriter, r *http.Request, papel, sub string) {
	if papel == papelVincularVendedor {
		seller := currentSeller(r)
		if seller == nil {
			http.Redirect(w, r, "/vendedor/login", http.StatusSeeOther)
			return
		}
		switch err := models.LinkSellerGoogle(seller.ID, sub); {
		case errors.Is(err, models.ErrGoogleAccountConflict):
			http.Redirect(w, r, "/vendedor?google=conflito", http.StatusSeeOther)
		case err != nil:
			log.Printf("vínculo Google do vendedor %d: %v", seller.ID, err)
			http.Redirect(w, r, "/vendedor?google=erro", http.StatusSeeOther)
		default:
			http.Redirect(w, r, "/vendedor?google=vinculado", http.StatusSeeOther)
		}
		return
	}

	seller, err := models.SellerByGoogle(sub)
	if err != nil {
		if !errors.Is(err, models.ErrSellerGoogleNotLinked) {
			log.Printf("login Google de vendedor: %v", err)
		}
		http.Redirect(w, r, "/vendedor/login?google=sem-vinculo", http.StatusSeeOther)
		return
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

// SellerGoogleUnlink - POST /vendedor/google/desvincular
func SellerGoogleUnlink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	seller := currentSeller(r)
	if err := models.UnlinkSellerGoogle(seller.ID); err != nil {
		log.Printf("desvincular Google do vendedor %d: %v", seller.ID, err)
	}
	http.Redirect(w, r, "/vendedor?google=desvinculado", http.StatusSeeOther)
}

var sellerGoogleAvisos = map[string]struct {
	texto string
	ok    bool
}{
	"vinculado":    {"Conta Google conectada. Da próxima vez, entre com o botão do Google.", true},
	"desvinculado": {"Conta Google desconectada. Entre com usuário e senha.", true},
	"conflito":     {"Essa conta Google já está ligada a outro vendedor.", false},
	"cancelado":    {"Login com Google cancelado. Nada foi alterado.", false},
	"erro":         {"Não foi possível falar com o Google agora. Tente de novo.", false},
	"indisponivel": {"O login com Google ainda não foi configurado pela loja.", false},
	"sem-vinculo":  {"Essa conta Google não está ligada a nenhum vendedor. Entre com usuário e senha e conecte o Google no Início do painel.", false},
}

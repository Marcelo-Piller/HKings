package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/vendas/loja/models"
)

// LGPD: Termos de Uso, Política de Privacidade, aceite registrado e os
// direitos do titular (acesso aos dados e exclusão da conta).

// lgpdData são os dados do controlador mostrados nos documentos.
func lgpdData() map[string]any {
	return map[string]any{
		"Controlador":   firstNonEmpty(models.GetSetting("lgpd_controlador"), models.GetSetting("store_name"), "Livredo"),
		"Documento":     models.GetSetting("lgpd_documento"),
		"EmailDPO":      models.GetSetting("lgpd_email"),
		"Encarregado":   models.GetSetting("lgpd_encarregado"),
		"Site":          firstNonEmpty(models.GetSetting("store_base_url"), "https://livredo.com.br"),
		"Versao":        models.TermsVersion,
		"VersaoLegivel": versaoLegivel(models.TermsVersion),
	}
}

func versaoLegivel(v string) string {
	if len(v) == 10 {
		return v[8:10] + "/" + v[5:7] + "/" + v[0:4]
	}
	return v
}

// TermsPage - GET /termos.
func TermsPage(w http.ResponseWriter, r *http.Request) {
	renderStore(w, r, "terms.html", lgpdData())
}

// PrivacyPage - GET /privacidade.
func PrivacyPage(w http.ResponseWriter, r *http.Request) {
	renderStore(w, r, "privacy.html", lgpdData())
}

// termsAccepted diz se o formulário veio com a caixa de aceite marcada.
func termsAccepted(r *http.Request) bool { return r.FormValue("aceite_termos") == "on" }

func recordConsent(r *http.Request, subject string, id int, origin string) {
	if err := models.RecordConsent(subject, id, clientIP(r), r.UserAgent(), origin); err != nil {
		log.Printf("aceite dos termos (%s %d): %v", subject, id, err)
	}
}

// consentGate devolve o destino depois do login: a página de aceite, se a
// pessoa ainda não aceitou a versão atual dos termos.
func consentGate(subject string, id int, target string) string {
	if id <= 0 || models.HasCurrentConsent(subject, id) {
		return target
	}
	return "/termos/aceitar?papel=" + subject + "&volta=" + url.QueryEscape(target)
}

// TermsAccept - GET/POST /termos/aceitar: aceite de quem já tem conta (conta
// criada pelo Google ou termos atualizados).
func TermsAccept(w http.ResponseWriter, r *http.Request) {
	subject := models.SubjectCustomer
	id := currentCustomerID(r)
	base := "/conta"
	if r.FormValue("papel") == models.SubjectSeller {
		subject, base = models.SubjectSeller, "/vendedor"
		id = 0
		if s := currentSeller(r); s != nil {
			id = s.ID
		}
	}
	if id <= 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	volta := safeReturnPath(r.FormValue("volta"))
	if volta == "" || strings.HasPrefix(volta, "/termos/aceitar") {
		volta = base
	}
	data := lgpdData()
	data["Papel"], data["Volta"] = subject, volta
	if r.Method == http.MethodPost {
		if !termsAccepted(r) {
			data["Error"] = "Para continuar, marque que leu e aceita os Termos de Uso e a Política de Privacidade."
		} else {
			recordConsent(r, subject, id, "aceite")
			http.Redirect(w, r, volta, http.StatusSeeOther)
			return
		}
	}
	data["Atualizacao"] = len(models.ListConsents(subject, id)) > 0
	renderStore(w, r, "terms_accept.html", data)
}

// requireConsent manda para a página de aceite quem ainda não aceitou.
func requireConsent(subject string, id int, w http.ResponseWriter, r *http.Request) bool {
	if models.HasCurrentConsent(subject, id) {
		return true
	}
	target := r.URL.Path
	if r.Method != http.MethodGet {
		target = map[string]string{models.SubjectCustomer: "/conta", models.SubjectSeller: "/vendedor"}[subject]
	} else if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, consentGate(subject, id, target), http.StatusSeeOther)
	return false
}

// ── Direitos do titular ──────────────────────────────────────────────────────

// CustomerPrivacy - GET /conta/privacidade: dados, aceites e exclusão.
func CustomerPrivacy(w http.ResponseWriter, r *http.Request) {
	id := currentCustomerID(r)
	c, err := models.GetCustomerByID(id)
	if err != nil {
		http.Redirect(w, r, "/conta/entrar", http.StatusSeeOther)
		return
	}
	data := lgpdData()
	data["Customer"] = c
	data["Aceites"] = models.ListConsents(models.SubjectCustomer, id)
	switch r.URL.Query().Get("aviso") {
	case "confirmar":
		data["Error"] = "Para excluir a conta, digite EXCLUIR no campo de confirmação."
	case "pedidos":
		data["Error"] = "Você tem pedidos pagos ainda não entregues. A conta pode ser excluída depois que eles chegarem."
	case "erro":
		data["Error"] = "Não foi possível excluir a conta agora. Tente de novo ou escreva para o encarregado de dados."
	}
	renderStore(w, r, "account_privacy.html", data)
}

// CustomerDataDownload - GET /conta/privacidade/dados.json (art. 18, II e V).
func CustomerDataDownload(w http.ResponseWriter, r *http.Request) {
	data, err := models.CustomerDataExport(currentCustomerID(r))
	if err != nil {
		http.Error(w, "não foi possível gerar o arquivo", http.StatusInternalServerError)
		return
	}
	data["gerado_em_versao_dos_termos"] = models.TermsVersion
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="meus-dados-livredo.json"`)
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(data)
}

// CustomerDelete - POST /conta/privacidade/excluir (art. 18, VI).
func CustomerDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if strings.ToUpper(strings.TrimSpace(r.FormValue("confirmacao"))) != "EXCLUIR" {
		http.Redirect(w, r, "/conta/privacidade?aviso=confirmar", http.StatusSeeOther)
		return
	}
	id := currentCustomerID(r)
	avatar, err := models.DeleteCustomerAccount(id)
	if err != nil {
		aviso := "erro"
		if errors.Is(err, models.ErrOpenOrders) {
			aviso = "pedidos"
		} else {
			log.Printf("exclusão da conta %d: %v", id, err)
		}
		http.Redirect(w, r, "/conta/privacidade?aviso="+aviso, http.StatusSeeOther)
		return
	}
	removeUploadedImage(avatar)
	log.Printf("LGPD: conta de cliente %d excluída a pedido do titular", id)
	sess, _ := store.Get(r, "session")
	delete(sess.Values, "customer_id")
	sess.Save(r, w)
	http.Redirect(w, r, "/?conta=excluida", http.StatusSeeOther)
}

// SellerDataDownload - GET /vendedor/meus-dados.json: dados do vendedor.
func SellerDataDownload(w http.ResponseWriter, r *http.Request) {
	s := currentSeller(r)
	products, _ := models.ListProductsBySeller(s.ID)
	var livros []map[string]any
	for _, p := range products {
		livros = append(livros, map[string]any{"id": p.ID, "titulo": p.Name, "preco": p.Price, "estoque": p.Stock, "formato": p.Format})
	}
	data := map[string]any{
		"conta": map[string]any{"id": s.ID, "nome": s.Name, "usuario": s.Username, "email": s.Email,
			"documento": s.Document, "tipo": s.ProfileLabel(), "foto": s.AvatarURL,
			"mercado_pago_conectado": s.MPConnected()},
		"livros":  livros,
		"aceites": models.ListConsents(models.SubjectSeller, s.ID),
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="meus-dados-vendedor-%d.json"`, s.ID))
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(data)
}

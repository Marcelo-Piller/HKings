package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/vendas/loja/models"
)

func isCustomerLoggedIn(r *http.Request) bool {
	return currentCustomerID(r) > 0
}

func currentCustomerID(r *http.Request) int {
	sess, _ := store.Get(r, "session")
	v, ok := sess.Values["customer_id"]
	if !ok {
		return 0
	}
	id, _ := v.(int)
	return id
}

// safeReturnPath aceita como destino depois do login só caminhos do próprio
// site. "//golpe.com" e "/\\golpe.com" são lidos pelo navegador como outro
// domínio, então são recusados: sem isso, um link de login legítimo
// mandaria a vítima, já logada, para uma página falsa.
func safeReturnPath(v string) string {
	if v == "" || len(v) > 300 || v[0] != '/' || strings.HasPrefix(v, "//") || strings.ContainsAny(v, "\\\r\n") {
		return ""
	}
	return v
}

// afterLogin é para onde o cliente vai depois de entrar.
func afterLogin(r *http.Request) string {
	if v := safeReturnPath(r.FormValue("volta")); v != "" {
		return v
	}
	return "/conta"
}

// loginPageData leva aos formulários de login e cadastro o destino da volta
// e o motivo do pedido de login.
func loginPageData(r *http.Request, data map[string]any) map[string]any {
	if data == nil {
		data = map[string]any{}
	}
	data["Volta"] = safeReturnPath(r.FormValue("volta"))
	if r.FormValue("motivo") == "ebook" {
		data["MotivoEbook"] = true
	}
	return data
}

// RequireCustomer é o middleware de proteção das rotas da área do cliente.
func RequireCustomer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isCustomerLoggedIn(r) {
			http.Redirect(w, r, "/conta/entrar", http.StatusSeeOther)
			return
		}
		// Baixar os dados e excluir a conta não dependem de aceitar os termos.
		if !strings.HasPrefix(r.URL.Path, "/conta/privacidade") && !requireConsent(models.SubjectCustomer, currentCustomerID(r), w, r) {
			return
		}
		next(w, r)
	}
}

// CustomerRegister - cadastro de novo cliente
func CustomerRegister(w http.ResponseWriter, r *http.Request) {
	if isCustomerLoggedIn(r) {
		http.Redirect(w, r, afterLogin(r), http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodPost {
		r.ParseForm()
		name := r.FormValue("nome")
		email := r.FormValue("email")
		password := r.FormValue("senha")
		confirm := r.FormValue("confirmar_senha")

		if !termsAccepted(r) {
			renderStore(w, r, "account_register.html", loginPageData(r, map[string]any{
				"Error": "Para criar a conta, aceite os Termos de Uso e a Política de Privacidade.",
				"Nome":  name, "Email": email,
			}))
			return
		}
		if name == "" || email == "" || len(password) < 12 {
			renderStore(w, r, "account_register.html", loginPageData(r, map[string]any{
				"Error": "Preencha nome, e-mail e uma senha com pelo menos 12 caracteres.",
				"Nome":  name, "Email": email,
			}))
			return
		}
		if password != confirm {
			renderStore(w, r, "account_register.html", loginPageData(r, map[string]any{
				"Error": "As senhas não coincidem.",
				"Nome":  name, "Email": email,
			}))
			return
		}

		id, err := models.CreateCustomer(name, email, password)
		if err != nil {
			msg := "Não foi possível criar a conta."
			if err == models.ErrEmailInUse {
				msg = "Este e-mail já está cadastrado."
			}
			renderStore(w, r, "account_register.html", loginPageData(r, map[string]any{
				"Error": msg,
				"Nome":  name, "Email": email,
			}))
			return
		}

		recordConsent(r, models.SubjectCustomer, int(id), "cadastro")
		sendWelcomeEmail(email, welcomeData{Nome: name})
		sess, _ := store.Get(r, "session")
		if err := rotateCSRFToken(sess); err != nil {
			http.Error(w, "não foi possível iniciar a sessão", http.StatusInternalServerError)
			return
		}
		sess.Values["customer_id"] = int(id)
		if err := sess.Save(r, w); err != nil {
			http.Error(w, "não foi possível salvar a sessão", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, afterLogin(r), http.StatusSeeOther)
		return
	}
	renderStore(w, r, "account_register.html", loginPageData(r, nil))
}

// CustomerLogin - login de cliente
func CustomerLogin(w http.ResponseWriter, r *http.Request) {
	if isCustomerLoggedIn(r) {
		http.Redirect(w, r, afterLogin(r), http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodPost {
		key := loginRateKey("customer", r)
		if allowed, retryIn := loginLimiter.Allowed(key); !allowed {
			renderStore(w, r, "account_login.html", loginPageData(r, map[string]any{"Error": blockedMessage(retryIn)}))
			return
		}

		r.ParseForm()
		email := r.FormValue("email")
		password := r.FormValue("senha")

		c, err := models.AuthenticateCustomer(email, password)
		if err != nil {
			loginLimiter.RegisterFailure(key)
			msg := "E-mail ou senha inválidos."
			if errors.Is(err, models.ErrUseGoogleLogin) {
				msg = "Esta conta entra com o Google. Use o botão “Continuar com o Google”."
			}
			renderStore(w, r, "account_login.html", loginPageData(r, map[string]any{
				"Error": msg,
				"Email": email,
			}))
			return
		}

		loginLimiter.Reset(key)
		sess, _ := store.Get(r, "session")
		if err := rotateCSRFToken(sess); err != nil {
			http.Error(w, "não foi possível iniciar a sessão", http.StatusInternalServerError)
			return
		}
		sess.Values["customer_id"] = c.ID
		loggedID := c.ID
		if err := sess.Save(r, w); err != nil {
			http.Error(w, "não foi possível salvar a sessão", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, consentGate(models.SubjectCustomer, loggedID, afterLogin(r)), http.StatusSeeOther)
		return
	}
	renderStore(w, r, "account_login.html", loginPageData(r, nil))
}

// CustomerLogout - logout de cliente
func CustomerLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	sess, _ := store.Get(r, "session")
	delete(sess.Values, "customer_id")
	delete(sess.Values, guestOrderIDsKey)
	_ = rotateCSRFToken(sess)
	sess.Save(r, w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// CustomerAccount - exibe e atualiza os dados da conta
func CustomerAccount(w http.ResponseWriter, r *http.Request) {
	c, err := models.GetCustomerByID(currentCustomerID(r))
	if err != nil {
		http.Redirect(w, r, "/conta/entrar", http.StatusSeeOther)
		return
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		name := r.FormValue("nome")
		phone := r.FormValue("telefone")
		address := r.FormValue("endereco")
		if err := models.UpdateCustomerProfile(c.ID, name, phone, address); err != nil {
			renderStore(w, r, "account.html", map[string]any{"Customer": c, "Error": "Não foi possível salvar os dados."})
			return
		}
		http.Redirect(w, r, "/conta?salvo=1", http.StatusSeeOther)
		return
	}

	renderStore(w, r, "account.html", map[string]any{
		"Customer":  c,
		"Saved":     r.URL.Query().Get("salvo") == "1",
		"FotoAviso": avatarAvisos[r.URL.Query().Get("foto")],
		"FotoOK":    r.URL.Query().Get("foto") == "ok" || r.URL.Query().Get("foto") == "removida",
		"GoogleAviso": map[string]string{
			"nova":      "Conta criada com o Google. Complete seu telefone e endereço para agilizar as compras.",
			"vinculada": "Sua conta agora entra com o Google. A senha antiga foi desativada por segurança.",
		}[r.URL.Query().Get("google")],
	})
}

// CustomerOrders - lista os pedidos do cliente logado
func CustomerOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := models.ListOrdersByCustomer(currentCustomerID(r))
	if err != nil {
		orders = []models.Order{}
	}
	renderStore(w, r, "account_orders.html", map[string]any{
		"Orders": orders,
	})
}

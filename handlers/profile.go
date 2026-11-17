package handlers

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/vendas/loja/models"
)

// Foto de perfil, avaliação de vendedores e denúncias.

// reportLimiter: no máximo 5 denúncias por cliente a cada 24 horas. Denúncia
// suspende vendedor na mão do admin, então não pode virar ferramenta de spam.
var reportLimiter = newRateLimiter(5, 24*time.Hour, 24*time.Hour)

// dict permite passar vários valores para um template parcial:
// {{template "avatar" dict "URL" .AvatarURL "Nome" .Name}}.
func dict(pairs ...any) map[string]any {
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		if key, ok := pairs[i].(string); ok {
			m[key] = pairs[i+1]
		}
	}
	return m
}

// initial devolve a primeira letra do nome, usada no avatar sem foto.
func initial(name string) string {
	for _, r := range strings.TrimSpace(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return string(unicode.ToUpper(r))
		}
	}
	return "?"
}

// updateAvatar trata o formulário de foto de perfil, comum a cliente e
// vendedor: "remover" volta para a inicial; senão grava a imagem enviada.
// Devolve o código do aviso mostrado na página de volta.
func updateAvatar(w http.ResponseWriter, r *http.Request, set func(string) (string, error)) string {
	r.Body = http.MaxBytesReader(w, r.Body, maxImageBytes+64<<10)
	if err := r.ParseMultipartForm(maxImageBytes); err != nil {
		return "invalida"
	}
	newURL := ""
	if r.FormValue("acao") != "remover" {
		uploaded, err := saveImageUpload(r, "foto")
		if err != nil {
			return "invalida"
		}
		newURL = uploaded
	}
	old, err := set(newURL)
	if err != nil {
		log.Printf("foto de perfil: %v", err)
		removeUploadedImage(newURL)
		return "erro"
	}
	if old != newURL {
		removeUploadedImage(old)
	}
	if newURL == "" {
		return "removida"
	}
	return "ok"
}

var avatarAvisos = map[string]string{
	"ok":       "Foto de perfil atualizada.",
	"removida": "Foto de perfil removida.",
	"invalida": "Envie uma imagem JPEG, PNG, WebP ou GIF de até 4 MB.",
	"erro":     "Não foi possível salvar a foto agora.",
}

// CustomerAvatar - POST /conta/foto
func CustomerAvatar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/conta", http.StatusSeeOther)
		return
	}
	id := currentCustomerID(r)
	aviso := updateAvatar(w, r, func(url string) (string, error) { return models.SetCustomerAvatar(id, url) })
	http.Redirect(w, r, "/conta?foto="+aviso, http.StatusSeeOther)
}

// SellerAvatar - POST /vendedor/foto
func SellerAvatar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/vendedor", http.StatusSeeOther)
		return
	}
	seller := currentSeller(r)
	aviso := updateAvatar(w, r, func(url string) (string, error) { return models.SetSellerAvatar(seller.ID, url) })
	http.Redirect(w, r, "/vendedor?foto="+aviso, http.StatusSeeOther)
}

// OrderReview - POST /pedido/avaliar: o comprador dá de 1 a 5 estrelas ao
// vendedor. As regras (dono do pedido, já enviado, uma vez só) ficam no model.
func OrderReview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/conta/pedidos", http.StatusSeeOther)
		return
	}
	orderID, _ := strconv.Atoi(r.FormValue("id"))
	stars, _ := strconv.Atoi(r.FormValue("estrelas"))
	volta := "/pedido?id=" + strconv.Itoa(orderID) + "&avaliacao="

	err := models.CreateReview(orderID, currentCustomerID(r), stars, r.FormValue("comentario"))
	switch {
	case err == nil:
		http.Redirect(w, r, volta+"ok", http.StatusSeeOther)
	case errors.Is(err, models.ErrAlreadyReviewed):
		http.Redirect(w, r, volta+"repetida", http.StatusSeeOther)
	case errors.Is(err, models.ErrReviewNotAllowed):
		http.Redirect(w, r, volta+"invalida", http.StatusSeeOther)
	default:
		log.Printf("avaliação do pedido #%d: %v", orderID, err)
		http.Redirect(w, r, volta+"erro", http.StatusSeeOther)
	}
}

var reviewAvisos = map[string]string{
	"ok":       "Obrigado! Sua avaliação ajuda outros leitores.",
	"repetida": "Este pedido já foi avaliado.",
	"invalida": "Escolha de 1 a 5 estrelas. Só é possível avaliar depois que o pedido é enviado.",
	"erro":     "Não foi possível salvar a avaliação agora.",
}

// ReportSeller - GET/POST /denunciar?vendedor=ID[&pedido=ID]
func ReportSeller(w http.ResponseWriter, r *http.Request) {
	sellerID, _ := strconv.Atoi(r.FormValue("vendedor"))
	seller, err := models.GetSeller(sellerID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	customerID := currentCustomerID(r)
	orderID, _ := strconv.Atoi(r.FormValue("pedido"))
	productID, _ := strconv.Atoi(r.FormValue("livro"))

	data := map[string]any{
		"Seller":  seller,
		"Reasons": models.ReportReasons,
		"OrderID": orderID,
		"Orders":  customerOrdersWithSeller(customerID, sellerID),
	}
	if productID > 0 {
		if p, err := models.GetProduct(productID); err == nil && p.SellerID == sellerID {
			data["Product"] = p
		} else {
			productID = 0
		}
	}

	if r.Method == http.MethodPost {
		data["Reason"] = r.FormValue("motivo")
		data["Description"] = r.FormValue("descricao")

		key := "denuncia|" + strconv.Itoa(customerID)
		if allowed, _ := reportLimiter.Allowed(key); !allowed {
			data["Error"] = "Você já enviou várias denúncias hoje. Nossa equipe está analisando; tente de novo amanhã."
			renderStore(w, r, "report.html", data)
			return
		}
		err := models.CreateReport(sellerID, customerID, orderID, productID, r.FormValue("motivo"), r.FormValue("descricao"))
		if err != nil {
			if errors.Is(err, models.ErrInvalidReport) {
				data["Error"] = capitalizeFirst(strings.TrimPrefix(err.Error(), models.ErrInvalidReport.Error()+": ")) + "."
			} else {
				log.Printf("denúncia contra vendedor %d: %v", sellerID, err)
				data["Error"] = "Não foi possível registrar a denúncia agora."
			}
			renderStore(w, r, "report.html", data)
			return
		}
		reportLimiter.RegisterFailure(key) // conta cada denúncia registrada
		data["Sent"] = true
	}
	renderStore(w, r, "report.html", data)
}

// customerOrdersWithSeller lista os pedidos do cliente com esse vendedor, para
// a denúncia poder apontar a compra envolvida.
func customerOrdersWithSeller(customerID, sellerID int) []models.Order {
	orders, err := models.ListOrdersByCustomer(customerID)
	if err != nil {
		return nil
	}
	var out []models.Order
	for _, o := range orders {
		if o.SellerID == sellerID {
			out = append(out, o)
		}
	}
	return out
}

// AdminReports - GET/POST /admin/denuncias
func AdminReports(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		switch r.FormValue("acao") {
		case "suspender", "reativar":
			sellerID, _ := strconv.Atoi(r.FormValue("vendedor"))
			if sellerID > 0 {
				if err := models.SetSellerActive(sellerID, r.FormValue("acao") == "reativar"); err != nil {
					log.Printf("denúncias: alterar vendedor %d: %v", sellerID, err)
				}
			}
		case "tirar_livro", "devolver_livro":
			// Resposta rápida a denúncia de pirataria: o livro some da loja na
			// hora, sem apagar nada; quem já comprou continua com o download.
			productID, _ := strconv.Atoi(r.FormValue("livro"))
			if productID > 0 {
				if err := models.SetProductActive(productID, r.FormValue("acao") == "devolver_livro"); err != nil {
					log.Printf("denúncias: alterar livro %d: %v", productID, err)
				}
			}
		default:
			id, _ := strconv.Atoi(r.FormValue("id"))
			if err := models.UpdateReport(id, r.FormValue("status"), r.FormValue("nota")); err != nil {
				log.Printf("denúncias: atualizar #%d: %v", id, err)
			}
		}
		http.Redirect(w, r, "/admin/denuncias?status="+r.FormValue("filtro"), http.StatusSeeOther)
		return
	}

	status := r.URL.Query().Get("status")
	reports, err := models.ListReports(status)
	if err != nil {
		log.Printf("denúncias: %v", err)
	}
	renderAdmin(w, r, "admin_reports.html", map[string]any{
		"Reports":  reports,
		"Statuses": models.ReportStatuses,
		"Filter":   status,
	})
}

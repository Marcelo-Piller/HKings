package handlers

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/vendas/loja/models"
)

// Autorização para vender e-books: o vendedor pede (autor ou editora, com a
// comprovação) e o admin aprova, recusa ou revoga. Ver models/digital_rights.go.

// SellerDigital - GET/POST /vendedor/ebooks
func SellerDigital(w http.ResponseWriter, r *http.Request) {
	seller := currentSeller(r)
	data := map[string]any{"Seller": seller}
	if r.Method == http.MethodPost {
		r.ParseForm()
		err := models.RequestDigitalRights(seller.ID, r.FormValue("tipo"),
			[]string{r.FormValue("autores")}, r.FormValue("comprovacao"))
		if err == nil && r.FormValue("declaro") != "on" {
			err = errors.New("confirme a declaração de direitos")
		}
		if err != nil {
			msg := err.Error()
			if _, detalhe, ok := strings.Cut(msg, ": "); ok && errors.Is(err, models.ErrDigitalRequest) {
				msg = detalhe
			}
			data["Error"] = capitalizeFirst(msg) + "."
			data["Form"] = map[string]string{
				"Tipo": r.FormValue("tipo"), "Autores": r.FormValue("autores"), "Comprovacao": r.FormValue("comprovacao"),
			}
		} else {
			http.Redirect(w, r, "/vendedor/ebooks?enviado=1", http.StatusSeeOther)
			return
		}
	}
	data["Direitos"] = models.GetDigitalRights(seller.ID)
	data["Enviado"] = r.URL.Query().Get("enviado") == "1"
	renderSeller(w, r, "seller_digital.html", data)
}

// AdminDigital - GET/POST /admin/ebooks: análise dos pedidos.
func AdminDigital(w http.ResponseWriter, r *http.Request) {
	filtro := r.FormValue("status")
	if r.Method == http.MethodPost {
		r.ParseForm()
		id, _ := strconv.Atoi(r.FormValue("vendedor"))
		aprovar := r.FormValue("acao") == "aprovar"
		if err := models.ReviewDigitalRights(id, aprovar, []string{r.FormValue("autores")}, r.FormValue("nota")); err != nil {
			msg := err.Error()
			if _, detalhe, ok := strings.Cut(msg, ": "); ok {
				msg = detalhe
			}
			renderAdminDigital(w, r, filtro, capitalizeFirst(msg)+".")
			return
		}
		log.Printf("e-books: vendedor %d %s pelo admin", id, map[bool]string{true: "aprovado", false: "recusado/revogado"}[aprovar])
		http.Redirect(w, r, "/admin/ebooks?status="+filtro, http.StatusSeeOther)
		return
	}
	renderAdminDigital(w, r, filtro, "")
}

func renderAdminDigital(w http.ResponseWriter, r *http.Request, filtro, errMsg string) {
	switch filtro {
	case models.DigitalPending, models.DigitalApproved, models.DigitalRejected:
	default:
		filtro = ""
	}
	pedidos, err := models.ListDigitalRights(filtro)
	if err != nil {
		log.Printf("e-books: listagem: %v", err)
	}
	data := map[string]any{"Pedidos": pedidos, "Filtro": filtro}
	if errMsg != "" {
		data["Error"] = errMsg
	}
	renderAdmin(w, r, "admin_digital.html", data)
}

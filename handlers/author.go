package handlers

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/vendas/loja/models"
)

// AuthorPage - GET /autor/{slug}: biografia e livros do autor.
func AuthorPage(w http.ResponseWriter, r *http.Request) {
	slug := strings.Trim(strings.TrimPrefix(r.URL.Path, "/autor/"), "/")
	if slug == "" || slug != models.AuthorSlug(slug) {
		// Endereço fora do formato (maiúsculas, acentos): manda ao certo.
		if s := models.AuthorSlug(slug); s != "" {
			http.Redirect(w, r, "/autor/"+s, http.StatusMovedPermanently)
			return
		}
		http.NotFound(w, r)
		return
	}
	author, err := models.GetAuthorPage(slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	renderStore(w, r, "author.html", map[string]any{
		"Author":    author,
		"CartCount": getCart(r).Count(),
	})
}

// authorAvisos são as mensagens depois de salvar a página do autor.
var authorAvisos = map[string]string{
	"ok":       "Página do autor salva.",
	"invalida": "A foto precisa ser JPEG, PNG, WebP ou GIF de até 4 MB.",
	"dados":    "Confira os dados: a biografia pode ter até 5.000 caracteres.",
	"erro":     "Não foi possível salvar agora. Tente de novo.",
}

// saveAuthorForm grava o formulário da página do autor e devolve o código
// do aviso (authorAvisos).
func saveAuthorForm(w http.ResponseWriter, r *http.Request, name string) string {
	r.Body = http.MaxBytesReader(w, r.Body, maxImageBytes+256<<10)
	if err := r.ParseMultipartForm(maxImageBytes); err != nil {
		return "invalida"
	}
	current := models.GetAuthor(name)
	a := *current
	a.Name = name
	a.Bio = r.FormValue("bio")
	a.Website = r.FormValue("site")

	newPhoto := ""
	switch uploaded, err := saveImageUpload(r, "foto"); {
	case err == nil:
		newPhoto, a.PhotoURL = uploaded, uploaded
	case errors.Is(err, ErrNoImage):
		if r.FormValue("remover_foto") == "on" {
			a.PhotoURL = ""
		}
	default:
		return "invalida"
	}
	if err := models.SaveAuthor(a); err != nil {
		removeUploadedImage(newPhoto)
		if errors.Is(err, models.ErrInvalidAuthor) {
			return "dados"
		}
		log.Printf("página do autor %q: %v", name, err)
		return "erro"
	}
	if current.PhotoURL != "" && current.PhotoURL != a.PhotoURL {
		removeUploadedImage(current.PhotoURL)
	}
	return "ok"
}

// SellerAuthors - GET/POST /vendedor/autores: páginas dos autores que o
// vendedor representa (direitos digitais aprovados).
func SellerAuthors(w http.ResponseWriter, r *http.Request) {
	seller := currentSeller(r)
	names := models.SellerAuthors(seller.ID)
	name := strings.TrimSpace(r.URL.Query().Get("nome"))
	if name != "" && !models.SellerCanEditAuthor(seller.ID, name) {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost {
		if name == "" {
			http.NotFound(w, r)
			return
		}
		aviso := saveAuthorForm(w, r, name)
		http.Redirect(w, r, "/vendedor/autores?nome="+url.QueryEscape(name)+"&aviso="+aviso, http.StatusSeeOther)
		return
	}
	data := map[string]any{
		"Seller":   seller,
		"Nomes":    names,
		"Direitos": models.GetDigitalRights(seller.ID),
	}
	if name == "" && len(names) == 1 {
		name = names[0]
	}
	if name != "" {
		data["Autor"] = models.GetAuthor(name)
		data["Nome"] = name
	}
	if aviso, ok := authorAvisos[r.URL.Query().Get("aviso")]; ok {
		data["Aviso"], data["AvisoOK"] = aviso, r.URL.Query().Get("aviso") == "ok"
	}
	renderSeller(w, r, "seller_authors.html", data)
}

// AdminAuthors - GET/POST /admin/autores: o admin edita qualquer autor.
func AdminAuthors(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("nome"))
	if r.Method == http.MethodPost {
		if models.AuthorSlug(name) == "" {
			http.NotFound(w, r)
			return
		}
		aviso := saveAuthorForm(w, r, name)
		http.Redirect(w, r, "/admin/autores?nome="+url.QueryEscape(name)+"&aviso="+aviso, http.StatusSeeOther)
		return
	}
	authors, err := models.ListAuthors()
	if err != nil {
		log.Printf("autores: %v", err)
	}
	data := map[string]any{"Autores": authors}
	if models.AuthorSlug(name) != "" {
		data["Autor"] = models.GetAuthor(name)
		data["Nome"] = name
	}
	if aviso, ok := authorAvisos[r.URL.Query().Get("aviso")]; ok {
		data["Aviso"], data["AvisoOK"] = aviso, r.URL.Query().Get("aviso") == "ok"
	}
	renderAdmin(w, r, "admin_authors.html", data)
}

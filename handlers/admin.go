package handlers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vendas/loja/database"
	"github.com/vendas/loja/models"
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

// saveUploadedImage salva o arquivo enviado e retorna o caminho público.
func saveUploadedImage(r *http.Request) string {
	fallback := r.FormValue("imagem_url")
	if fallback == "" {
		fallback = "/static/img/placeholder.svg"
	}

	file, header, err := r.FormFile("imagem")
	if err != nil {
		return fallback
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true}
	if !allowed[ext] {
		return fallback
	}

	os.MkdirAll(filepath.Join("static", "img", "uploads"), 0755)

	filename := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	dst := filepath.Join("static", "img", "uploads", filename)

	out, err := os.Create(dst)
	if err != nil {
		return fallback
	}
	defer out.Close()
	io.Copy(out, file)

	return "/static/img/uploads/" + filename
}

// AdminLogin - página de login
func AdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		user := r.FormValue("usuario")
		pass := r.FormValue("senha")

		var id int
		err := database.DB.QueryRow(`SELECT id FROM admins WHERE username=? AND password=?`, user, pass).Scan(&id)
		if err == nil {
			sess, _ := store.Get(r, "session")
			sess.Values["admin"] = true
			sess.Save(r, w)
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}
		renderAdmin(w, "admin_login.html", map[string]any{"Error": "Usuário ou senha inválidos"})
		return
	}
	renderAdmin(w, "admin_login.html", nil)
}

// AdminLogout - logout
func AdminLogout(w http.ResponseWriter, r *http.Request) {
	sess, _ := store.Get(r, "session")
	delete(sess.Values, "admin")
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

	renderAdmin(w, "admin_dashboard.html", map[string]any{
		"Orders":        orders,
		"Products":      products,
		"TotalOrders":   len(orders),
		"TotalRevenue":  totalRevenue,
		"TotalProducts": len(products),
	})
}

// AdminProducts - lista de produtos
func AdminProducts(w http.ResponseWriter, r *http.Request) {
	products, _ := models.ListAllProducts()
	renderAdmin(w, "admin_products.html", map[string]any{"Products": products})
}

// AdminProductNew - formulário novo produto
func AdminProductNew(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseMultipartForm(5 << 20)
		price, _ := strconv.ParseFloat(r.FormValue("preco"), 64)
		stock, _ := strconv.Atoi(r.FormValue("estoque"))
		p := models.Product{
			Name:        r.FormValue("nome"),
			Description: r.FormValue("descricao"),
			Price:       price,
			Stock:       stock,
			ImageURL:    saveUploadedImage(r),
			Category:    r.FormValue("categoria"),
			Active:      true,
		}
		if err := models.CreateProduct(p); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		http.Redirect(w, r, "/admin/produtos", http.StatusSeeOther)
		return
	}
	renderAdmin(w, "admin_product_form.html", map[string]any{"Product": nil})
}

// AdminProductEdit - editar produto
func AdminProductEdit(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	if r.Method == http.MethodPost {
		r.ParseMultipartForm(5 << 20)
		price, _ := strconv.ParseFloat(r.FormValue("preco"), 64)
		stock, _ := strconv.Atoi(r.FormValue("estoque"))
		active := r.FormValue("ativo") == "on"

		imageURL := saveUploadedImage(r)
		if imageURL == "/static/img/placeholder.svg" {
			if cur, err := models.GetProduct(id); err == nil {
				imageURL = cur.ImageURL
			}
		}

		p := models.Product{
			ID:          id,
			Name:        r.FormValue("nome"),
			Description: r.FormValue("descricao"),
			Price:       price,
			Stock:       stock,
			ImageURL:    imageURL,
			Category:    r.FormValue("categoria"),
			Active:      active,
		}
		if err := models.UpdateProduct(p); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		http.Redirect(w, r, "/admin/produtos", http.StatusSeeOther)
		return
	}
	p, err := models.GetProduct(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	renderAdmin(w, "admin_product_form.html", map[string]any{"Product": p})
}

// AdminProductDelete - desativa produto
func AdminProductDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/produtos", http.StatusSeeOther)
		return
	}
	r.ParseForm()
	id, _ := strconv.Atoi(r.FormValue("id"))
	models.DeleteProduct(id)
	http.Redirect(w, r, "/admin/produtos", http.StatusSeeOther)
}

// AdminOrders - lista de pedidos
func AdminOrders(w http.ResponseWriter, r *http.Request) {
	orders, _ := models.ListOrders()
	renderAdmin(w, "admin_orders.html", map[string]any{"Orders": orders})
}

// AdminOrderDetail - detalhes de um pedido
func AdminOrderDetail(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	if r.Method == http.MethodPost {
		r.ParseForm()
		models.UpdateOrderStatus(id, r.FormValue("status"))
		http.Redirect(w, r, "/admin/pedidos", http.StatusSeeOther)
		return
	}
	order, err := models.GetOrder(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	renderAdmin(w, "admin_order_detail.html", map[string]any{"Order": order})
}

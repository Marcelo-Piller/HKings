package handlers

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/vendas/loja/models"
	"github.com/vendas/loja/services"
)

// pages armazena um template set por página, cada um parseado apenas com seu layout.
var pages map[string]*template.Template

// Init pré-compila cada página junto com seu respectivo layout.
// Isso evita o conflito de nomes ({{define "content"}}) que ocorre
// quando todos os templates são parseados num único conjunto.
func Init() {
	os.MkdirAll(filepath.Join("static", "img", "uploads"), 0755)

	pages = make(map[string]*template.Template)

	layout := filepath.Join("templates", "layout.html")
	adminLayout := filepath.Join("templates", "admin", "layout.html")

	storePages := []string{
		"home.html", "product.html", "cart.html",
		"checkout.html", "order_confirm.html",
	}
	for _, p := range storePages {
		pages[p] = template.Must(template.ParseFiles(layout, filepath.Join("templates", p)))
	}

	adminPages := []string{
		"admin_dashboard.html", "admin_products.html", "admin_product_form.html",
		"admin_orders.html", "admin_order_detail.html",
	}
	for _, p := range adminPages {
		pages[p] = template.Must(template.ParseFiles(adminLayout, filepath.Join("templates", p)))
	}

	pages["admin_login.html"] = template.Must(template.ParseFiles(filepath.Join("templates", "admin_login.html")))
}

// renderStore executa um template de loja usando o layout principal.
func renderStore(w http.ResponseWriter, page string, data any) {
	t, ok := pages[page]
	if !ok {
		http.Error(w, "template não encontrado: "+page, 500)
		return
	}
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("renderStore %s: %v", page, err)
	}
}

// renderAdmin executa um template de admin usando o layout admin.
func renderAdmin(w http.ResponseWriter, page string, data any) {
	t, ok := pages[page]
	if !ok {
		http.Error(w, "template não encontrado: "+page, 500)
		return
	}
	var err error
	if page == "admin_login.html" {
		err = t.Execute(w, data)
	} else {
		err = t.ExecuteTemplate(w, "admin_layout", data)
	}
	if err != nil {
		log.Printf("renderAdmin %s: %v", page, err)
	}
}

func getCart(r *http.Request) *models.Cart {
	sess, _ := store.Get(r, "session")
	raw, ok := sess.Values["cart"]
	if !ok {
		return &models.Cart{}
	}
	data, ok := raw.([]byte)
	if !ok {
		return &models.Cart{}
	}
	var cart models.Cart
	if err := json.Unmarshal(data, &cart); err != nil {
		return &models.Cart{}
	}
	return &cart
}

func saveCart(w http.ResponseWriter, r *http.Request, cart *models.Cart) {
	sess, _ := store.Get(r, "session")
	data, _ := json.Marshal(cart)
	sess.Values["cart"] = data
	sess.Save(r, w)
}

// Home - página inicial com lista de produtos
func Home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	category := r.URL.Query().Get("categoria")
	search := r.URL.Query().Get("q")

	products, err := models.ListProducts(category)
	if err != nil {
		log.Printf("ListProducts: %v", err)
		products = []models.Product{}
	}

	if search != "" {
		var filtered []models.Product
		for _, p := range products {
			if containsCI(p.Name, search) || containsCI(p.Description, search) {
				filtered = append(filtered, p)
			}
		}
		products = filtered
	}

	categories, _ := models.ListCategories()
	cart := getCart(r)

	renderStore(w, "home.html", map[string]any{
		"Products":    products,
		"Categories":  categories,
		"SelectedCat": category,
		"Search":      search,
		"CartCount":   cart.Count(),
	})
}

// ProductDetail - página de detalhes do produto
func ProductDetail(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := models.GetProduct(id)
	if err != nil || !p.Active {
		http.NotFound(w, r)
		return
	}
	cart := getCart(r)
	renderStore(w, "product.html", map[string]any{
		"Product":   p,
		"CartCount": cart.Count(),
	})
}

// CartView - exibe o carrinho
func CartView(w http.ResponseWriter, r *http.Request) {
	cart := getCart(r)
	renderStore(w, "cart.html", map[string]any{
		"Cart":      cart,
		"CartCount": cart.Count(),
	})
}

// CartAdd - adiciona produto ao carrinho
func CartAdd(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id, _ := strconv.Atoi(r.FormValue("id"))
	qty, _ := strconv.Atoi(r.FormValue("qty"))
	if qty <= 0 {
		qty = 1
	}
	p, err := models.GetProduct(id)
	if err != nil {
		http.Error(w, "produto não encontrado", 400)
		return
	}
	cart := getCart(r)
	cart.Add(*p, qty)
	saveCart(w, r, cart)
	http.Redirect(w, r, "/carrinho", http.StatusSeeOther)
}

// CartUpdate - atualiza quantidade no carrinho
func CartUpdate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id, _ := strconv.Atoi(r.FormValue("id"))
	qty, _ := strconv.Atoi(r.FormValue("qty"))
	cart := getCart(r)
	cart.UpdateQty(id, qty)
	saveCart(w, r, cart)
	http.Redirect(w, r, "/carrinho", http.StatusSeeOther)
}

// CartRemove - remove item do carrinho
func CartRemove(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id, _ := strconv.Atoi(r.FormValue("id"))
	cart := getCart(r)
	cart.Remove(id)
	saveCart(w, r, cart)
	http.Redirect(w, r, "/carrinho", http.StatusSeeOther)
}

// CheckoutView - formulário de checkout (GET)
func CheckoutView(w http.ResponseWriter, r *http.Request) {
	cart := getCart(r)
	if cart.Count() == 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	renderStore(w, "checkout.html", map[string]any{
		"Cart":      cart,
		"CartCount": cart.Count(),
	})
}

// CheckoutProcess - processa o pedido (POST)
func CheckoutProcess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/checkout", http.StatusSeeOther)
		return
	}
	r.ParseForm()
	cart := getCart(r)
	if cart.Count() == 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	order := &models.Order{
		CustomerName:  r.FormValue("nome"),
		CustomerEmail: r.FormValue("email"),
		CustomerPhone: r.FormValue("telefone"),
		Address:       r.FormValue("endereco"),
		Total:         cart.Total(),
	}
	for _, item := range cart.Items {
		order.Items = append(order.Items, models.OrderItem{
			ProductID:   item.Product.ID,
			ProductName: item.Product.Name,
			Quantity:    item.Quantity,
			Price:       item.Product.Price,
		})
	}

	id, err := models.CreateOrder(order)
	if err != nil {
		http.Error(w, "erro ao criar pedido: "+err.Error(), 500)
		return
	}
	cart.Clear()
	saveCart(w, r, cart)

	desc := "Pedido #" + strconv.FormatInt(id, 10) + " - MinhaLoja"
	payURL, err := services.CreatePaymentLink(order.Total, desc, id)
	if err != nil {
		log.Printf("InfinitPay: %v", err)
		http.Redirect(w, r, "/pedido?id="+strconv.FormatInt(id, 10), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, payURL, http.StatusSeeOther)
}

// OrderConfirm - confirmação do pedido
func OrderConfirm(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	order, err := models.GetOrder(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	renderStore(w, "order_confirm.html", map[string]any{
		"Order":     order,
		"CartCount": 0,
	})
}

func containsCI(s, sub string) bool {
	sl := len(s)
	subl := len(sub)
	if subl > sl {
		return false
	}
	for i := 0; i <= sl-subl; i++ {
		match := true
		for j := 0; j < subl; j++ {
			a, b := s[i+j], sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

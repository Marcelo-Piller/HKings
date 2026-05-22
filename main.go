package main

import (
	"log"
	"net/http"
	"os"

	"github.com/vendas/loja/database"
	"github.com/vendas/loja/handlers"
)

func main() {
	database.Init("./loja.db")
	handlers.Init()

	mux := http.NewServeMux()

	// Arquivos estáticos
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Loja pública
	mux.HandleFunc("/", handlers.Home)
	mux.HandleFunc("/produto", handlers.ProductDetail)
	mux.HandleFunc("/carrinho", handlers.CartView)
	mux.HandleFunc("/carrinho/add", handlers.CartAdd)
	mux.HandleFunc("/carrinho/update", handlers.CartUpdate)
	mux.HandleFunc("/carrinho/remove", handlers.CartRemove)
	mux.HandleFunc("/checkout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			handlers.CheckoutProcess(w, r)
		} else {
			handlers.CheckoutView(w, r)
		}
	})
	mux.HandleFunc("/pedido", handlers.OrderConfirm)

	// Admin
	mux.HandleFunc("/admin/login", handlers.AdminLogin)
	mux.HandleFunc("/admin/logout", handlers.AdminLogout)
	mux.HandleFunc("/admin", handlers.RequireAdmin(handlers.AdminDashboard))
	mux.HandleFunc("/admin/produtos", handlers.RequireAdmin(handlers.AdminProducts))
	mux.HandleFunc("/admin/produtos/novo", handlers.RequireAdmin(handlers.AdminProductNew))
	mux.HandleFunc("/admin/produtos/editar", handlers.RequireAdmin(handlers.AdminProductEdit))
	mux.HandleFunc("/admin/produtos/excluir", handlers.RequireAdmin(handlers.AdminProductDelete))
	mux.HandleFunc("/admin/pedidos", handlers.RequireAdmin(handlers.AdminOrders))
	mux.HandleFunc("/admin/pedidos/detalhe", handlers.RequireAdmin(handlers.AdminOrderDetail))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("🛍️  Servidor iniciado em http://localhost:%s", port)
	log.Printf("🔑  Admin: http://localhost:%s/admin/login  (usuário: admin / senha: admin123)", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

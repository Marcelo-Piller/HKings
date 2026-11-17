package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vendas/loja/database"
	"github.com/vendas/loja/handlers"
	"github.com/vendas/loja/models"
)

func main() {
	// -backup grava um snapshot consistente e encerra, sem subir o servidor.
	// Copiar o arquivo .db por fora não serve: com WAL, as gravações recentes
	// ficam no arquivo -wal e a cópia sai incompleta.
	backupTo := flag.String("backup", "", "grava uma cópia consistente do banco no caminho informado e encerra")
	flag.Parse()

	// O caminho só é usado quando DB_PATH não está definida; em Docker a
	// variável aponta para o volume persistente.
	database.Init("./data/loja.db")

	if *backupTo != "" {
		if err := database.Backup(*backupTo); err != nil {
			log.Fatal("backup: ", err)
		}
		log.Printf("backup gravado em %s", *backupTo)
		database.DB.Close()
		return
	}

	if err := models.MigrateLegacyPasswordHashes(); err != nil {
		log.Fatal("não foi possível proteger credenciais existentes:", err)
	}
	if err := models.MigrateSensitiveSettings(); err != nil {
		log.Fatal("não foi possível proteger configurações sensíveis existentes:", err)
	}
	if err := models.EnsureInitialAdmin(); err != nil {
		log.Fatal(err)
	}
	handlers.Init()

	go handlers.BackfillEbookPages()
	handlers.StartDispatchJobs()

	// Pedido sem pagamento há 3 dias é cancelado e devolve o estoque. Roda já
	// na subida e depois a cada hora.
	go func() {
		for {
			if n, err := models.ExpireUnpaidOrders(72); err != nil {
				log.Printf("expiração de pedidos: %v", err)
			} else if n > 0 {
				log.Printf("%d pedido(s) sem pagamento cancelado(s); estoque devolvido", n)
			}
			time.Sleep(time.Hour)
		}
	}()

	mux := http.NewServeMux()

	// Arquivos estáticos
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Saúde do serviço (usado pelo healthcheck do Docker)
	mux.HandleFunc("/healthz", handlers.HealthCheck)

	// Loja pública
	mux.HandleFunc("/", handlers.Home)
	mux.HandleFunc("/produto", handlers.ProductDetail)
	mux.HandleFunc("/livro/amostra", handlers.EbookSample)
	mux.HandleFunc("/livro/amostra/pagina", handlers.ComicSamplePage)
	mux.HandleFunc("/livro/amostra/audio", handlers.AudioSample)
	mux.HandleFunc("/autor/", handlers.AuthorPage)
	mux.HandleFunc("/termos", handlers.TermsPage)
	mux.HandleFunc("/privacidade", handlers.PrivacyPage)
	mux.HandleFunc("/termos/aceitar", handlers.TermsAccept)
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
	mux.HandleFunc("/pedido/comprovante", handlers.OrderReceiptPDF)
	mux.HandleFunc("/pedido/comprovante/email", handlers.OrderReceiptEmail)
	mux.HandleFunc("/pedido/avaliar", handlers.RequireCustomer(handlers.OrderReview))
	mux.HandleFunc("/frete/cotar", handlers.ShippingQuote)
	mux.HandleFunc("/denunciar", handlers.RequireCustomer(handlers.ReportSeller))

	// Conta do cliente
	mux.HandleFunc("/conta/cadastro", handlers.CustomerRegister)
	mux.HandleFunc("/conta/entrar", handlers.CustomerLogin)
	mux.HandleFunc("/conta/sair", handlers.CustomerLogout)
	mux.HandleFunc("/conta", handlers.RequireCustomer(handlers.CustomerAccount))
	mux.HandleFunc("/conta/pedidos", handlers.RequireCustomer(handlers.CustomerOrders))
	mux.HandleFunc("/conta/foto", handlers.RequireCustomer(handlers.CustomerAvatar))
	mux.HandleFunc("/conta/biblioteca", handlers.RequireCustomer(handlers.CustomerLibrary))
	mux.HandleFunc("/conta/privacidade", handlers.RequireCustomer(handlers.CustomerPrivacy))
	mux.HandleFunc("/conta/privacidade/dados.json", handlers.RequireCustomer(handlers.CustomerDataDownload))
	mux.HandleFunc("/conta/privacidade/excluir", handlers.RequireCustomer(handlers.CustomerDelete))
	mux.HandleFunc("/conta/biblioteca/baixar", handlers.RequireCustomer(handlers.EbookDownload))
	mux.HandleFunc("/conta/biblioteca/ouvir", handlers.RequireCustomer(handlers.CustomerListen))
	mux.HandleFunc("/conta/biblioteca/faixa", handlers.RequireCustomer(handlers.CustomerAudioTrack))
	mux.HandleFunc("/conta/biblioteca/audiobook.zip", handlers.RequireCustomer(handlers.CustomerAudioZip))
	mux.HandleFunc("/conta/google", handlers.CustomerGoogleStart)
	mux.HandleFunc("/conta/google/retorno", handlers.CustomerGoogleCallback)

	// Pagamento (Mercado Pago)
	mux.HandleFunc("/pix", handlers.PixPaymentPage)
	mux.HandleFunc("/pix/status", handlers.PixStatusAPI)
	mux.HandleFunc("/pagamento/ir", handlers.PaymentRedirect)
	mux.HandleFunc("/pagamento/retorno", handlers.PaymentReturn)
	mux.HandleFunc("/webhook/mp", handlers.MercadoPagoWebhook)

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
	mux.HandleFunc("/admin/categorias", handlers.RequireAdmin(handlers.AdminCategories))
	mux.HandleFunc("/admin/configuracoes", handlers.RequireAdmin(handlers.AdminSettings))
	mux.HandleFunc("/admin/configuracoes/email-teste", handlers.RequireAdmin(handlers.AdminEmailTest))
	mux.HandleFunc("/admin/configuracoes/boas-vindas", handlers.RequireAdmin(handlers.AdminWelcomeBatch))
	mux.HandleFunc("/admin/vendedores", handlers.RequireAdmin(handlers.AdminSellers))
	mux.HandleFunc("/admin/vendedores/reativar", handlers.RequireAdmin(handlers.AdminReactivateSeller))
	mux.HandleFunc("/admin/denuncias", handlers.RequireAdmin(handlers.AdminReports))
	mux.HandleFunc("/admin/envio", handlers.RequireAdmin(handlers.AdminShipping))
	mux.HandleFunc("/admin/ebooks", handlers.RequireAdmin(handlers.AdminDigital))
	mux.HandleFunc("/admin/autores", handlers.RequireAdmin(handlers.AdminAuthors))
	mux.HandleFunc("/admin/importar", handlers.RequireAdmin(handlers.AdminImport))
	mux.HandleFunc("/admin/importar/publicar", handlers.RequireAdmin(handlers.AdminImportPublish))
	mux.HandleFunc("/admin/importar/modelo.csv", handlers.RequireAdmin(handlers.ImportTemplate))
	mux.HandleFunc("/admin/produtos/exportar.csv", handlers.RequireAdmin(handlers.AdminCatalogExport))
	mux.HandleFunc("/admin/produtos/massa", handlers.RequireAdmin(handlers.AdminBulkEdit))
	mux.HandleFunc("/admin/produtos/audio", handlers.RequireAdmin(handlers.AdminAudio))
	mux.HandleFunc("/admin/isbn", handlers.RequireAdmin(handlers.ISBNLookup))

	// Painel do vendedor
	mux.HandleFunc("/vendedor/cadastro", handlers.SellerRegister)
	mux.HandleFunc("/vendedor/login", handlers.SellerLogin)
	mux.HandleFunc("/vendedor/logout", handlers.SellerLogout)
	mux.HandleFunc("/vendedor", handlers.RequireSeller(handlers.SellerDashboard))
	mux.HandleFunc("/vendedor/produtos", handlers.RequireSeller(handlers.SellerProducts))
	mux.HandleFunc("/vendedor/produtos/novo", handlers.RequireSeller(handlers.SellerProductNew))
	mux.HandleFunc("/vendedor/produtos/editar", handlers.RequireSeller(handlers.SellerProductEdit))
	mux.HandleFunc("/vendedor/produtos/excluir", handlers.RequireSeller(handlers.SellerProductDelete))
	mux.HandleFunc("/vendedor/pedidos", handlers.RequireSeller(handlers.SellerOrders))
	mux.HandleFunc("/vendedor/pedidos/detalhe", handlers.RequireSeller(handlers.SellerOrderDetail))
	mux.HandleFunc("/vendedor/foto", handlers.RequireSeller(handlers.SellerAvatar))
	mux.HandleFunc("/vendedor/perfil", handlers.RequireSeller(handlers.SellerProfileUpdate))
	mux.HandleFunc("/vendedor/envio", handlers.RequireSeller(handlers.SellerShipping))
	mux.HandleFunc("/vendedor/ebooks", handlers.RequireSeller(handlers.SellerDigital))
	mux.HandleFunc("/vendedor/autores", handlers.RequireSeller(handlers.SellerAuthors))
	mux.HandleFunc("/vendedor/meus-dados.json", handlers.RequireSeller(handlers.SellerDataDownload))
	mux.HandleFunc("/vendedor/importar", handlers.RequireSeller(handlers.SellerImport))
	mux.HandleFunc("/vendedor/importar/publicar", handlers.RequireSeller(handlers.SellerImportPublish))
	mux.HandleFunc("/vendedor/importar/modelo.csv", handlers.RequireSeller(handlers.ImportTemplate))
	mux.HandleFunc("/vendedor/produtos/exportar.csv", handlers.RequireSeller(handlers.SellerCatalogExport))
	mux.HandleFunc("/vendedor/produtos/massa", handlers.RequireSeller(handlers.SellerBulkEdit))
	mux.HandleFunc("/vendedor/produtos/audio", handlers.RequireSeller(handlers.SellerAudio))
	mux.HandleFunc("/vendedor/isbn", handlers.RequireSeller(handlers.ISBNLookup))
	mux.HandleFunc("/vendedor/google/desvincular", handlers.RequireSeller(handlers.SellerGoogleUnlink))

	// Conta Melhor Envio (vendedor ou admin; quem é fica conferido no handler)
	mux.HandleFunc("/melhorenvio/conectar", handlers.MEConnect)
	mux.HandleFunc("/melhorenvio/retorno", handlers.MECallback)
	mux.HandleFunc("/melhorenvio/desconectar", handlers.MEDisconnect)

	// Conexão da conta Mercado Pago do vendedor (split de pagamentos)
	mux.HandleFunc("/vendedor/mercadopago/conectar", handlers.RequireSeller(handlers.SellerMPConnect))
	mux.HandleFunc("/vendedor/mercadopago/retorno", handlers.RequireSeller(handlers.SellerMPCallback))
	mux.HandleFunc("/vendedor/mercadopago/desconectar", handlers.RequireSeller(handlers.SellerMPDisconnect))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           handlers.WithSecurity(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// Encerramento gracioso: sem ele, o `docker stop` mata o processo no meio de
	// uma escrita e pode deixar o banco com trabalho pendente no WAL.
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("🛍️  Servidor iniciado na porta %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-shutdown
	log.Print("encerrando servidor...")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("encerramento forçado: %v", err)
	}
	if database.DB != nil {
		if err := database.DB.Close(); err != nil {
			log.Printf("erro ao fechar banco: %v", err)
		}
	}
	log.Print("servidor encerrado")
}

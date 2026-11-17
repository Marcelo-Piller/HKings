package database

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

// dataDir é a pasta do banco. Arquivos privados (os e-books) ficam ao lado
// dele, no mesmo volume persistente, e nunca dentro de static/.
var dataDir = "."

// DataDir devolve a pasta onde o banco está gravado.
func DataDir() string { return dataDir }

// legacyDBPath é onde o banco ficava antes de passar a morar em ./data.
const legacyDBPath = "./loja.db"

// dbPath resolve o arquivo do banco. Em produção (Docker) o caminho aponta para
// um volume persistente, de modo que recriar o container não apague os dados.
//
// Quando nada é informado e o banco novo ainda não existe, um ./loja.db de uma
// execução anterior continua sendo usado: mudar o padrão não pode dar a
// impressão de que os pedidos e clientes sumiram.
func dbPath(fallback string) string {
	if p := strings.TrimSpace(os.Getenv("DB_PATH")); p != "" {
		return p
	}
	if _, err := os.Stat(fallback); err == nil {
		return fallback
	}
	if _, err := os.Stat(legacyDBPath); err == nil {
		log.Printf("usando o banco existente em %s; para adotar o novo padrão, mova-o para %s", legacyDBPath, fallback)
		return legacyDBPath
	}
	return fallback
}

// dsn monta a string de conexão. O driver modernc.org/sqlite só reconhece
// PRAGMAs no formato _pragma=nome(valor); os parâmetros _journal_mode e
// _foreign_keys pertencem ao driver mattn/go-sqlite3 e eram ignorados aqui,
// deixando o banco sem WAL, sem chaves estrangeiras e sem tempo de espera.
func dsn(path string) string {
	pragmas := []string{
		"journal_mode(WAL)",   // leituras não bloqueiam durante uma escrita
		"foreign_keys(1)",     // integridade referencial de fato aplicada
		"busy_timeout(5000)",  // espera 5s em vez de falhar com "database is locked"
		"synchronous(NORMAL)", // seguro com WAL e muito mais rápido que FULL
	}
	query := make([]string, 0, len(pragmas))
	for _, p := range pragmas {
		query = append(query, "_pragma="+url.QueryEscape(p))
	}
	return "file:" + path + "?" + strings.Join(query, "&")
}

func Init(fallbackPath string) {
	path := dbPath(fallbackPath)
	dataDir = filepath.Dir(path)

	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			log.Fatal("erro ao criar diretório do banco:", err)
		}
	}

	var err error
	DB, err = sql.Open("sqlite", dsn(path))
	if err != nil {
		log.Fatal("erro ao abrir banco:", err)
	}

	// SQLite aceita um único escritor. Limitar o pool evita contenção e erros
	// de bloqueio sob concorrência, mantendo leituras paralelas via WAL.
	DB.SetMaxOpenConns(8)
	DB.SetMaxIdleConns(4)
	DB.SetConnMaxLifetime(time.Hour)

	if err = DB.Ping(); err != nil {
		log.Fatal("erro ao conectar banco:", err)
	}

	restrictPermissions(path)
	verifyPragmas()
	migrate()
}

// restrictPermissions impede que outros usuários do sistema leiam o arquivo do
// banco, que contém dados pessoais de clientes e segredos criptografados.
func restrictPermissions(path string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		file := path + suffix
		if _, err := os.Stat(file); err == nil {
			if err := os.Chmod(file, 0o600); err != nil {
				log.Printf("aviso: não foi possível restringir permissões de %s: %v", file, err)
			}
		}
	}
}

// verifyPragmas confirma que a configuração crítica foi aplicada de verdade.
// Falhar aqui é melhor que descobrir em produção que as chaves estrangeiras
// nunca estiveram ativas.
func verifyPragmas() {
	var journal string
	if err := DB.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		log.Fatal("não foi possível ler journal_mode:", err)
	}
	if !strings.EqualFold(journal, "wal") {
		log.Fatalf("journal_mode=%s; esperado WAL", journal)
	}

	var foreignKeys int
	if err := DB.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		log.Fatal("não foi possível ler foreign_keys:", err)
	}
	if foreignKeys != 1 {
		log.Fatal("foreign_keys desligado; integridade referencial não seria aplicada")
	}
}

// Backup grava uma cópia consistente do banco usando VACUUM INTO, que funciona
// com o servidor em execução e produz um arquivo já compactado e íntegro.
func Backup(destination string) error {
	if dir := filepath.Dir(destination); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("arquivo de backup já existe: %s", destination)
	}
	if _, err := DB.Exec(`VACUUM INTO ?`, destination); err != nil {
		return err
	}
	return os.Chmod(destination, 0o600)
}

// HealthCheck confirma que o banco responde, para o endpoint /healthz e para o
// healthcheck do Docker.
func HealthCheck() error {
	var one int
	return DB.QueryRow(`SELECT 1`).Scan(&one)
}

func migrate() {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS products (
			id        INTEGER PRIMARY KEY AUTOINCREMENT,
			name      TEXT    NOT NULL,
			description TEXT  NOT NULL DEFAULT '',
			price     REAL    NOT NULL,
			stock     INTEGER NOT NULL DEFAULT 0,
			image_url TEXT    NOT NULL DEFAULT '',
			category  TEXT    NOT NULL DEFAULT 'Geral',
			active    INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS orders (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			customer_name   TEXT NOT NULL,
			customer_email  TEXT NOT NULL,
			customer_phone  TEXT NOT NULL DEFAULT '',
			address         TEXT NOT NULL DEFAULT '',
			total           REAL NOT NULL,
			status          TEXT NOT NULL DEFAULT 'pendente',
			pix_payment_id  TEXT NOT NULL DEFAULT '',
			pix_qrcode      TEXT NOT NULL DEFAULT '',
			pix_qrcode_img  TEXT NOT NULL DEFAULT '',
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS order_items (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			order_id   INTEGER NOT NULL REFERENCES orders(id),
			product_id INTEGER NOT NULL REFERENCES products(id),
			product_name TEXT NOT NULL,
			quantity   INTEGER NOT NULL,
			price      REAL NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS admins (
			id       INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sellers (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			name       TEXT NOT NULL,
			username   TEXT NOT NULL UNIQUE,
			password   TEXT NOT NULL,
			active     INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS customers (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			name          TEXT NOT NULL,
			email         TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			phone         TEXT NOT NULL DEFAULT '',
			address       TEXT NOT NULL DEFAULT '',
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		// Categorias deixam de ser texto livre digitado pelo vendedor e passam a
		// ser uma lista curada pelo administrador.
		`CREATE TABLE IF NOT EXISTS categories (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			name       TEXT NOT NULL UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		// Colunas adicionais para bancos existentes (erro silencioso se já existir)
		`ALTER TABLE orders ADD COLUMN pix_payment_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN pix_qrcode     TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN pix_qrcode_img TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN customer_id    INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE orders ADD COLUMN payment_method TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN mp_preference_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN mp_checkout_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN paid_amount_cents INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE products ADD COLUMN seller_id INTEGER NOT NULL DEFAULT 0`,
		// Estado do exemplar (novo/usado). Vazio nos produtos antigos: a loja
		// não presume "novo" para um livro que o vendedor nunca classificou.
		`ALTER TABLE products ADD COLUMN item_condition TEXT NOT NULL DEFAULT ''`,
		// Produto excluído que já foi vendido fica arquivado em vez de apagado,
		// para não quebrar o histórico dos pedidos.
		`ALTER TABLE products ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`,
		// Split de pagamentos: cada vendedor conecta a própria conta do Mercado
		// Pago. Os tokens dão acesso à conta dele e ficam criptografados, com a
		// mesma chave dos demais segredos (SETTINGS_ENCRYPTION_KEY).
		`ALTER TABLE sellers ADD COLUMN mp_user_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sellers ADD COLUMN mp_access_token TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sellers ADD COLUMN mp_refresh_token TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sellers ADD COLUMN mp_token_expires_at DATETIME`,
		// Quem recebe o pedido (0 = o próprio site) e a comissão combinada no
		// momento da compra, para a confirmação conferir contra o que foi pago.
		`ALTER TABLE orders ADD COLUMN seller_id INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE orders ADD COLUMN marketplace_fee_cents INTEGER NOT NULL DEFAULT 0`,
		// Conta Mercado Pago que deve receber este pedido, gravada ao criar a
		// cobrança. A confirmação só aceita pagamento recebido nessa conta.
		`ALTER TABLE orders ADD COLUMN mp_collector_id TEXT NOT NULL DEFAULT ''`,
		// Momento da confirmação do pagamento, impresso no comprovante.
		`ALTER TABLE orders ADD COLUMN paid_at DATETIME`,
		// Foto de perfil de clientes e vendedores (caminho em /static/img/uploads).
		`ALTER TABLE customers ADD COLUMN avatar_url TEXT NOT NULL DEFAULT ''`,
		// Livros digitais. product_format separa físico de digital; o arquivo do
		// e-book fica fora de static/ (ver DataDir) e só sai pelo download de
		// quem comprou. rights_declared_at registra quando o vendedor declarou
		// ter os direitos da obra ao enviar o arquivo.
		`ALTER TABLE products ADD COLUMN product_format TEXT NOT NULL DEFAULT 'fisico'`,
		`ALTER TABLE products ADD COLUMN author TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE products ADD COLUMN ebook_file TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE products ADD COLUMN ebook_type TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE products ADD COLUMN ebook_size INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE products ADD COLUMN rights_declared_at DATETIME`,
		`CREATE INDEX IF NOT EXISTS idx_products_format ON products(product_format)`,
		// Contra pirataria: só vende e-book o vendedor aprovado pelo admin como
		// autor(a) (com os nomes com que publica) ou editora parceira. O hash do
		// arquivo impede que o mesmo PDF/EPUB seja posto à venda duas vezes.
		`ALTER TABLE sellers ADD COLUMN digital_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sellers ADD COLUMN digital_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sellers ADD COLUMN digital_authors TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sellers ADD COLUMN digital_request TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sellers ADD COLUMN digital_note TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sellers ADD COLUMN digital_updated_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE products ADD COLUMN ebook_sha256 TEXT NOT NULL DEFAULT ''`,
		// Amostra de leitura do e-book: páginas do arquivo e escolha do vendedor.
		`ALTER TABLE products ADD COLUMN ebook_pages INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE products ADD COLUMN preview_pages TEXT NOT NULL DEFAULT ''`,
		// Ficha bibliográfica (importação por planilha e busca por ISBN).
		`ALTER TABLE products ADD COLUMN isbn TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE products ADD COLUMN publisher TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE products ADD COLUMN language TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_products_isbn ON products(seller_id, isbn) WHERE isbn <> ''`,
		// Lotes de importação por planilha (prévia antes de publicar) e cache
		// das consultas de ISBN.
		`CREATE TABLE IF NOT EXISTS import_batches (
			id         TEXT PRIMARY KEY,
			seller_id  INTEGER NOT NULL,
			status     TEXT NOT NULL,
			done       INTEGER NOT NULL DEFAULT 0,
			total      INTEGER NOT NULL DEFAULT 0,
			rows_json  TEXT NOT NULL DEFAULT '[]',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		// LGPD: registro de cada aceite dos Termos e da Política de
		// Privacidade (nunca apagado) e a versão aceita em cada pedido.
		`CREATE TABLE IF NOT EXISTS consents (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			subject     TEXT NOT NULL,
			subject_id  INTEGER NOT NULL,
			version     TEXT NOT NULL,
			accepted_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			ip          TEXT NOT NULL DEFAULT '',
			user_agent  TEXT NOT NULL DEFAULT '',
			origin      TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_consents_subject ON consents(subject, subject_id, version)`,
		`ALTER TABLE orders ADD COLUMN terms_version TEXT NOT NULL DEFAULT ''`,
		// Audiobook: faixas enviadas pelo vendedor, preços das edições e a
		// edição comprada em cada item do pedido (ebook, audio ou combo).
		`CREATE TABLE IF NOT EXISTS audio_tracks (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			product_id   INTEGER NOT NULL REFERENCES products(id),
			position     INTEGER NOT NULL,
			title        TEXT NOT NULL,
			file         TEXT NOT NULL,
			format       TEXT NOT NULL,
			size         INTEGER NOT NULL,
			duration_sec INTEGER NOT NULL DEFAULT 0,
			sha256       TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audio_tracks_product ON audio_tracks(product_id, position)`,
		`CREATE INDEX IF NOT EXISTS idx_audio_tracks_sha ON audio_tracks(sha256)`,
		`ALTER TABLE products ADD COLUMN audio_price REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE products ADD COLUMN bundle_price REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE products ADD COLUMN audio_rights_at TEXT`,
		`ALTER TABLE order_items ADD COLUMN edition TEXT NOT NULL DEFAULT ''`,
		// Prazo de despacho, andamento do envio e rastreio do Melhor Envio.
		`ALTER TABLE orders ADD COLUMN ship_deadline TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN ready_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN posted_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN delivered_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN late INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE orders ADD COLUMN track_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN track_checked_at TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_orders_seller_deadline ON orders(seller_id, ship_deadline)`,
		// Pausa da loja por atraso: os livros saem da vitrine, a conta continua.
		`ALTER TABLE sellers ADD COLUMN listing_paused INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sellers ADD COLUMN paused_reason TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sellers ADD COLUMN paused_at TEXT`,
		`ALTER TABLE sellers ADD COLUMN penalty_reset_at TEXT`,
		`CREATE TABLE IF NOT EXISTS dispatch_notices (
			order_id INTEGER NOT NULL,
			kind     TEXT NOT NULL,
			sent_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (order_id, kind)
		)`,
		// Quem já recebeu o e-mail de boas-vindas (para não mandar duas vezes).
		`ALTER TABLE customers ADD COLUMN welcome_sent_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sellers ADD COLUMN welcome_sent_at TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS isbn_cache (
			isbn       TEXT PRIMARY KEY,
			data_json  TEXT NOT NULL,
			found      INTEGER NOT NULL,
			fetched_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		// Login de vendedor com Google: vinculado só de dentro do painel.
		`ALTER TABLE sellers ADD COLUMN google_sub TEXT NOT NULL DEFAULT ''`,
		// Que tipo de vendedor é (editora, sebo, livraria, autor, leitor): dado
		// de perfil, para conhecer quem vende na loja.
		`ALTER TABLE sellers ADD COLUMN seller_profile TEXT NOT NULL DEFAULT ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_sellers_google ON sellers(google_sub) WHERE google_sub <> ''`,
		`CREATE INDEX IF NOT EXISTS idx_products_ebook_sha ON products(ebook_sha256) WHERE ebook_sha256 <> ''`,
		// Frete (Melhor Envio). Peso e medidas do livro físico embalado; zero
		// em livros antigos, que usam o pacote padrão de livro na cotação.
		`ALTER TABLE products ADD COLUMN weight_g INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE products ADD COLUMN height_cm INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE products ADD COLUMN width_cm INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE products ADD COLUMN length_cm INTEGER NOT NULL DEFAULT 0`,
		// De onde cada vendedor despacha (seller_id 0 = o próprio site) e a
		// conta Melhor Envio dele, com os tokens criptografados.
		`CREATE TABLE IF NOT EXISTS shipping_origins (
			seller_id INTEGER PRIMARY KEY,
			postal_code TEXT NOT NULL DEFAULT '',
			street TEXT NOT NULL DEFAULT '',
			number TEXT NOT NULL DEFAULT '',
			complement TEXT NOT NULL DEFAULT '',
			district TEXT NOT NULL DEFAULT '',
			city TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL DEFAULT '',
			phone TEXT NOT NULL DEFAULT '',
			me_access_token TEXT NOT NULL DEFAULT '',
			me_refresh_token TEXT NOT NULL DEFAULT '',
			me_expires_at TEXT NOT NULL DEFAULT '',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		// Frete escolhido no checkout, endereço estruturado para a etiqueta e
		// o que a etiqueta gerou (pedido no Melhor Envio, PDF e rastreio).
		`ALTER TABLE orders ADD COLUMN shipping_cents INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE orders ADD COLUMN shipping_service_id INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE orders ADD COLUMN shipping_service TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN shipping_days INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE orders ADD COLUMN ship_postal_code TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN ship_street TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN ship_number TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN ship_complement TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN ship_district TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN ship_city TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN ship_state TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN customer_document TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN me_order_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN label_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orders ADD COLUMN tracking_code TEXT NOT NULL DEFAULT ''`,
		// Login com Google: google_sub é o identificador fixo da conta Google.
		// Vazio para quem entra só com senha; o índice único vale só para os preenchidos.
		`ALTER TABLE customers ADD COLUMN google_sub TEXT NOT NULL DEFAULT ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_customers_google ON customers(google_sub) WHERE google_sub <> ''`,
		`ALTER TABLE sellers ADD COLUMN avatar_url TEXT NOT NULL DEFAULT ''`,
		// Avaliação do vendedor: uma por pedido (order_id UNIQUE), feita pelo
		// cliente que comprou. É o que impede alguém de inflar a própria nota.
		`CREATE TABLE IF NOT EXISTS seller_reviews (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			order_id    INTEGER NOT NULL UNIQUE REFERENCES orders(id),
			seller_id   INTEGER NOT NULL REFERENCES sellers(id),
			customer_id INTEGER NOT NULL REFERENCES customers(id),
			stars       INTEGER NOT NULL CHECK (stars BETWEEN 1 AND 5),
			comment     TEXT NOT NULL DEFAULT '',
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reviews_seller ON seller_reviews(seller_id)`,
		// Denúncias contra vendedores. Quem denunciou só é visível ao admin.
		// Páginas de autor: biografia e foto, pelo nome normalizado (slug).
		`CREATE TABLE IF NOT EXISTS authors (
			slug       TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			bio        TEXT NOT NULL DEFAULT '',
			photo_url  TEXT NOT NULL DEFAULT '',
			website    TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS seller_reports (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			seller_id   INTEGER NOT NULL REFERENCES sellers(id),
			customer_id INTEGER NOT NULL REFERENCES customers(id),
			order_id    INTEGER NOT NULL DEFAULT 0,
			reason      TEXT NOT NULL,
			description TEXT NOT NULL,
			status      TEXT NOT NULL DEFAULT 'aberta',
			admin_note  TEXT NOT NULL DEFAULT '',
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reports_status ON seller_reports(status)`,
		`CREATE INDEX IF NOT EXISTS idx_reports_seller ON seller_reports(seller_id)`,
		// Denúncia pode apontar o livro (ex.: e-book pirata), para o admin tirá-lo da loja.
		`ALTER TABLE seller_reports ADD COLUMN product_id INTEGER NOT NULL DEFAULT 0`,
		// E-mail e CPF/CNPJ do vendedor, para identificação e recuperação de
		// conta. NOT NULL DEFAULT '' preserva contas antigas sem exigir
		// preenchimento retroativo; os índices abaixo, parciais (WHERE <> ''),
		// garantem unicidade sem colidir com essas contas legadas em branco.
		`ALTER TABLE sellers ADD COLUMN email    TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sellers ADD COLUMN document TEXT NOT NULL DEFAULT ''`,
		// Índices: sem eles toda listagem de pedido, produto ou consulta de
		// pagamento fazia varredura completa da tabela.
		`CREATE INDEX IF NOT EXISTS idx_products_active_category ON products(active, category)`,
		`CREATE INDEX IF NOT EXISTS idx_products_seller ON products(seller_id)`,
		`CREATE INDEX IF NOT EXISTS idx_products_condition ON products(active, item_condition)`,
		`CREATE INDEX IF NOT EXISTS idx_orders_customer ON orders(customer_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sellers_mp_user ON sellers(mp_user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status)`,
		`CREATE INDEX IF NOT EXISTS idx_orders_pix_payment ON orders(pix_payment_id)`,
		`CREATE INDEX IF NOT EXISTS idx_orders_preference ON orders(mp_preference_id)`,
		`CREATE INDEX IF NOT EXISTS idx_order_items_order ON order_items(order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_order_items_product ON order_items(product_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_sellers_email ON sellers(email) WHERE email <> ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_sellers_document ON sellers(document) WHERE document <> ''`,
		// Remove contas de demonstração publicamente conhecidas. O primeiro
		// administrador deve ser provisionado por variáveis de ambiente.
		`DELETE FROM admins WHERE username='admin' AND password='admin123'`,
		`DELETE FROM sellers WHERE username='vendedor' AND password='vendedor123'`,
		// Gêneros iniciais apenas em banco vazio. "Geral" continua existindo
		// como destino padrão de produto sem gênero definido.
		`INSERT INTO categories (name)
		 SELECT 'Clássicos' WHERE NOT EXISTS (SELECT 1 FROM categories)
		 UNION ALL SELECT 'Romance' WHERE NOT EXISTS (SELECT 1 FROM categories)
		 UNION ALL SELECT 'Fantasia' WHERE NOT EXISTS (SELECT 1 FROM categories)
		 UNION ALL SELECT 'Poesia' WHERE NOT EXISTS (SELECT 1 FROM categories)
		 UNION ALL SELECT 'Não ficção' WHERE NOT EXISTS (SELECT 1 FROM categories)
		 UNION ALL SELECT 'Infantojuvenil' WHERE NOT EXISTS (SELECT 1 FROM categories)
		 UNION ALL SELECT 'Mangá' WHERE NOT EXISTS (SELECT 1 FROM categories)
		 UNION ALL SELECT 'HQ' WHERE NOT EXISTS (SELECT 1 FROM categories)
		 UNION ALL SELECT 'Geral' WHERE NOT EXISTS (SELECT 1 FROM categories)`,
		// Absorve categorias que já existiam nos produtos, para não perder nada
		// ao migrar de texto livre para lista curada.
		`INSERT OR IGNORE INTO categories (name)
		 SELECT DISTINCT TRIM(category) FROM products WHERE TRIM(category) <> ''`,
		// Mangá e HQ ganharam atalho no cabeçalho, então precisam existir também
		// em bancos criados antes deles. A marca em settings faz a inclusão
		// acontecer uma única vez: se o admin apagar a categoria depois, ela
		// não reaparece a cada reinício.
		`INSERT OR IGNORE INTO categories (name)
		 SELECT 'Mangá' WHERE NOT EXISTS (SELECT 1 FROM settings WHERE key='migracao_generos_manga_hq')
		 UNION ALL SELECT 'HQ' WHERE NOT EXISTS (SELECT 1 FROM settings WHERE key='migracao_generos_manga_hq')`,
		`INSERT OR IGNORE INTO settings (key, value) VALUES ('migracao_generos_manga_hq', '1')`,
		// A loja passou a se chamar Livredo. Só troca os nomes antigos: um nome
		// personalizado pelo admin continua como está.
		`UPDATE settings SET value='Livredo' WHERE key='store_name' AND TRIM(value) IN ('Mercado Livro', 'MercadoLivro', 'Folhetin', 'Livraria Pasquim')`,
	}
	for _, q := range queries {
		if _, err := DB.Exec(q); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			log.Printf("migrate: %v", err)
		}
	}
	if seedDemoEnabled() {
		if _, err := DB.Exec(demoProducts); err != nil {
			log.Printf("migrate: livros de exemplo: %v", err)
		}
	}
}

// seedDemoEnabled decide se uma loja vazia ganha os livros de exemplo. Em
// produção, não: eles apareceriam à venda por dinheiro de verdade sem existir
// no estoque. SEED_DEMO=true força a semeadura, e false a desliga.
func seedDemoEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SEED_DEMO"))) {
	case "true":
		return true
	case "false":
		return false
	}
	return !strings.EqualFold(os.Getenv("APP_ENV"), "production")
}

// demoProducts só entra num banco sem nenhum produto. São clássicos
// brasileiros em domínio público, para a vitrine de uma instalação de teste já
// parecer uma livraria.
const demoProducts = `INSERT INTO products (name, description, price, stock, category, image_url, item_condition)
		 SELECT 'Dom Casmurro', 'Bentinho revisita a própria vida para tentar responder à pergunta que o persegue: Capitu o traiu? Machado de Assis em sua ironia mais fina.', 39.90, 12, 'Clássicos', '/static/img/placeholder.svg', 'novo'
		 WHERE NOT EXISTS (SELECT 1 FROM products)
		 UNION ALL SELECT 'Memórias Póstumas de Brás Cubas', 'Um defunto autor narra a própria vida com humor e desencanto. O romance que inaugurou o Realismo no Brasil.', 42.90, 8, 'Clássicos', '/static/img/placeholder.svg', 'novo'
		 WHERE NOT EXISTS (SELECT 1 FROM products)
		 UNION ALL SELECT 'O Cortiço', 'Aluísio Azevedo transforma um cortiço do Rio de Janeiro em personagem vivo, pulsante e implacável.', 34.90, 15, 'Romance', '/static/img/placeholder.svg', 'novo'
		 WHERE NOT EXISTS (SELECT 1 FROM products)
		 UNION ALL SELECT 'Iracema', 'A lenda da virgem dos lábios de mel, em prosa poética de José de Alencar sobre o encontro de dois mundos.', 29.90, 3, 'Romance', '/static/img/placeholder.svg', 'usado'
		 WHERE NOT EXISTS (SELECT 1 FROM products)
		 UNION ALL SELECT 'Os Sertões', 'Euclides da Cunha narra a Guerra de Canudos entre a reportagem, a ciência e a epopeia.', 54.90, 6, 'Não ficção', '/static/img/placeholder.svg', 'usado'
		 WHERE NOT EXISTS (SELECT 1 FROM products)
		 UNION ALL SELECT 'Eu', 'O único livro de Augusto dos Anjos: versos sombrios, científicos e inesquecíveis.', 27.90, 10, 'Poesia', '/static/img/placeholder.svg', 'novo'
		 WHERE NOT EXISTS (SELECT 1 FROM products)`

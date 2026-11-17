package handlers

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vendas/loja/models"
)

// Audiobook: o vendedor envia as faixas e define os preços; quem compra ouve
// na biblioteca (player por faixa, lembrando onde parou) ou baixa. Os
// arquivos ficam fora de static/, em DataDir/audiobooks, e só saem por aqui.

func audioDir() string { return filepath.Join(filepath.Dir(ebookDir()), "audiobooks") }

// audioDownloadLimiter: baixar faixa por faixa de um livro longo passa fácil
// das 30 por hora dos e-books.
var audioDownloadLimiter = newRateLimiter(400, time.Hour, time.Hour)

// ── Painel: faixas e preços ──────────────────────────────────────────────────

func SellerAudio(w http.ResponseWriter, r *http.Request) { audioPage(sellerOwner(r), w, r) }
func AdminAudio(w http.ResponseWriter, r *http.Request)  { audioPage(adminOwner(r), w, r) }

var audioAvisos = map[string]string{
	"enviada":  "Faixa enviada.",
	"precos":   "Preços salvos.",
	"excluida": "Faixa excluída.",
	"ok":       "Alteração salva.",
}

func audioPage(o catalogOwner, w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	p, err := models.GetProduct(id)
	if err != nil || !p.IsDigital() || (o.ID != 0 && p.SellerID != o.ID) {
		http.NotFound(w, r)
		return
	}
	self := fmt.Sprintf("%s/produtos/audio?id=%d", o.Base, p.ID)
	data := map[string]any{"Base": o.Base, "Product": p, "Direitos": models.GetDigitalRights(p.SellerID)}
	if r.Method == http.MethodPost {
		aviso, msg := audioAction(r, p)
		if msg == "" {
			http.Redirect(w, r, self+"&aviso="+aviso, http.StatusSeeOther)
			return
		}
		data["Erro"] = msg
		p, _ = models.GetProduct(id)
		data["Product"] = p
	}
	tracks, err := models.ListAudioTracks(p.ID)
	if err != nil {
		log.Printf("audiobook %d: %v", p.ID, err)
	}
	var total int64
	for _, t := range tracks {
		total += t.Size
	}
	data["Faixas"] = tracks
	data["Total"] = models.DurationLabel(p.AudioSeconds)
	data["Espaco"] = fmt.Sprintf("%s de 2 GB", strings.TrimSpace(sizeLabel(total)))
	data["DireitosAudio"] = models.AudioRightsDeclared(p.ID)
	if a, ok := audioAvisos[r.URL.Query().Get("aviso")]; ok {
		data["Aviso"] = a
	}
	o.render(w, r, o.page+"audio.html", data)
}

func sizeLabel(n int64) string {
	switch {
	case n <= 0:
		return "0 MB"
	case n < 1<<30:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	}
	return strings.Replace(fmt.Sprintf("%.2f GB", float64(n)/(1<<30)), ".", ",", 1)
}

// audioAction trata o formulário: enviar faixa, preços, mover, renomear,
// excluir. Devolve o aviso de sucesso ou a mensagem de erro.
func audioAction(r *http.Request, p *models.Product) (string, string) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return "", "Não foi possível receber o arquivo. Ele precisa ter até 300 MB."
		}
		defer r.MultipartForm.RemoveAll()
	} else {
		r.ParseForm()
	}
	approved := models.GetDigitalRights(p.SellerID).Approved()
	switch r.FormValue("acao") {
	case "enviar":
		if !approved {
			return "", "Só quem tem a venda de e-books aprovada pode enviar audiobook."
		}
		if r.FormValue("direitos_audio") != "on" && !models.AudioRightsDeclared(p.ID) {
			return "", "Confirme que você tem os direitos de áudio da obra e da narração."
		}
		if err := saveAudioUpload(r, p.ID); err != nil {
			return "", audioErrorMessage(err)
		}
		if r.FormValue("direitos_audio") == "on" {
			models.SetAudioPrices(p.ID, p.AudioPrice, p.BundlePrice, true)
		}
		return "enviada", ""
	case "precos":
		audio, errA := parseOptionalPrice(r.FormValue("preco_audio"))
		bundle, errB := parseOptionalPrice(r.FormValue("preco_combo"))
		if errA != nil || errB != nil {
			return "", "Preço inválido: use, por exemplo, 39,90 (ou deixe em branco)."
		}
		if audio > 0 && p.AudioTracks == 0 {
			return "", "Envie as faixas do audiobook antes de colocá-lo à venda."
		}
		if err := models.SetAudioPrices(p.ID, audio, bundle, false); err != nil {
			return "", capitalizeFirst(strings.TrimPrefix(err.Error(), models.ErrInvalidProduct.Error()+": ")) + "."
		}
		return "precos", ""
	case "subir", "descer":
		delta := -1
		if r.FormValue("acao") == "descer" {
			delta = 1
		}
		trackID, _ := strconv.Atoi(r.FormValue("faixa"))
		if err := models.MoveAudioTrack(p.ID, trackID, delta); err != nil {
			return "", "Faixa não encontrada."
		}
		return "ok", ""
	case "renomear":
		trackID, _ := strconv.Atoi(r.FormValue("faixa"))
		if err := models.RenameAudioTrack(p.ID, trackID, r.FormValue("titulo")); err != nil {
			return "", "O título da faixa precisa ter de 1 a 120 caracteres."
		}
		return "ok", ""
	case "excluir":
		trackID, _ := strconv.Atoi(r.FormValue("faixa"))
		file, err := models.DeleteAudioTrack(p.ID, trackID)
		if err != nil {
			return "", "Faixa não encontrada."
		}
		removeAudioFile(file)
		// Sem faixas, o audiobook sai de venda.
		if tracks, _ := models.ListAudioTracks(p.ID); len(tracks) == 0 {
			models.SetAudioPrices(p.ID, 0, 0, false)
		}
		return "excluida", ""
	}
	return "", "Ação inválida."
}

func parseOptionalPrice(s string) (float64, error) {
	if strings.TrimSpace(s) == "" {
		return 0, nil
	}
	return models.ParsePriceBR(s)
}

func audioErrorMessage(err error) string {
	switch {
	case errors.Is(err, models.ErrInvalidAudio), errors.Is(err, models.ErrAudioTooBig), errors.Is(err, models.ErrAudioDuplicate):
		return capitalizeFirst(err.Error()) + "."
	case errors.Is(err, errNoAudio):
		return "Escolha o arquivo de áudio da faixa."
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return capitalizeFirst(models.ErrInvalidAudio.Error()) + "."
	}
	log.Printf("envio de áudio: %v", err)
	return "Não foi possível salvar o áudio. Tente de novo."
}

var errNoAudio = errors.New("sem arquivo de áudio")

// saveAudioUpload grava a faixa enviada no campo "audio", confere o tipo pelo
// conteúdo, calcula hash e duração e registra no audiobook.
func saveAudioUpload(r *http.Request, productID int) error {
	file, header, err := r.FormFile("audio")
	if err != nil {
		return errNoAudio
	}
	defer file.Close()
	if header.Size > models.MaxAudioTrackBytes {
		return models.ErrInvalidAudio
	}
	head := make([]byte, 64)
	n, _ := io.ReadFull(file, head)
	kind, err := models.DetectAudioType(head[:n])
	if err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := os.MkdirAll(audioDir(), 0o700); err != nil {
		return err
	}
	name, err := randomFileName("." + kind)
	if err != nil {
		return err
	}
	dest := filepath.Join(audioDir(), name)
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(file, models.MaxAudioTrackBytes+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || written > models.MaxAudioTrackBytes {
		os.Remove(dest)
		if copyErr == nil && closeErr == nil {
			return models.ErrInvalidAudio
		}
		return errors.Join(copyErr, closeErr)
	}
	title := strings.TrimSpace(r.FormValue("titulo"))
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename))
	}
	t := models.AudioTrack{ProductID: productID, Title: title, File: name, Format: kind, Size: written,
		DurationSec: models.AudioDuration(dest, kind), SHA256: hex.EncodeToString(h.Sum(nil))}
	if err := models.AddAudioTrack(t); err != nil {
		os.Remove(dest)
		return err
	}
	return nil
}

func removeAudioFile(name string) {
	if !models.ValidAudioFileName(name) || models.AudioFileInUse(name) {
		return
	}
	if err := os.Remove(filepath.Join(audioDir(), name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("não foi possível apagar o áudio %s: %v", name, err)
	}
}

// ── Amostra grátis ───────────────────────────────────────────────────────────

// audioSampleURL é o endereço da amostra: os primeiros minutos da primeira
// faixa, se ela for MP3 (dá para cortar em qualquer ponto).
func audioSampleURL(p *models.Product, tracks []models.AudioTrack) string {
	if len(tracks) == 0 || tracks[0].Format != models.AudioMP3 {
		return ""
	}
	return fmt.Sprintf("/livro/amostra/audio?id=%d&v=%d", p.ID, tracks[0].ID)
}

// AudioSample - GET /livro/amostra/audio?id=N: início da 1ª faixa, de graça.
func AudioSample(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	p, err := models.GetProduct(id)
	if err != nil || !p.Active || !p.HasAudio() || !models.GetDigitalRights(p.SellerID).Approved() {
		http.NotFound(w, r)
		return
	}
	tracks, err := models.ListAudioTracks(p.ID)
	if err != nil || len(tracks) == 0 || tracks[0].Format != models.AudioMP3 || !models.ValidAudioFileName(tracks[0].File) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(audioDir(), tracks[0].File)
	limit, ok := models.MP3SampleBytes(path, models.AudioSampleSeconds)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Minute))
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, "", time.Time{}, io.NewSectionReader(f, 0, limit))
}

// ── Biblioteca: ouvir e baixar ───────────────────────────────────────────────

// CustomerListen - GET /conta/biblioteca/ouvir?livro=N: player do audiobook.
func CustomerListen(w http.ResponseWriter, r *http.Request) {
	customerID := currentCustomerID(r)
	productID, _ := strconv.Atoi(r.URL.Query().Get("livro"))
	if !models.CustomerOwnsAudio(customerID, productID) {
		http.NotFound(w, r)
		return
	}
	p, err := models.GetProduct(productID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	tracks, _ := models.ListAudioTracks(productID)
	renderStore(w, r, "account_listen.html", map[string]any{"Product": p, "Faixas": tracks})
}

// CustomerAudioTrack - GET /conta/biblioteca/faixa?livro=N&faixa=K[&baixar=1]:
// toca (com Range, para pular para qualquer ponto) ou baixa uma faixa.
func CustomerAudioTrack(w http.ResponseWriter, r *http.Request) {
	customerID := currentCustomerID(r)
	productID, _ := strconv.Atoi(r.URL.Query().Get("livro"))
	trackID, _ := strconv.Atoi(r.URL.Query().Get("faixa"))
	t, err := models.GetAudioTrackForCustomer(customerID, productID, trackID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	download := r.URL.Query().Get("baixar") == "1"
	if download {
		key := "audio|" + strconv.Itoa(customerID)
		if allowed, _ := audioDownloadLimiter.Allowed(key); !allowed {
			http.Redirect(w, r, "/conta/biblioteca?aviso=limite", http.StatusSeeOther)
			return
		}
		audioDownloadLimiter.RegisterFailure(key)
	}
	f, err := os.Open(filepath.Join(audioDir(), t.File))
	if err != nil {
		log.Printf("faixa %d do audiobook %d: %v", t.ID, productID, err)
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "arquivo indisponível", http.StatusInternalServerError)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(60 * time.Minute))
	w.Header().Set("Content-Type", models.AudioContentType(t.Format))
	w.Header().Set("Cache-Control", "private, no-store")
	if download {
		name := strings.TrimSuffix(downloadFileName(fmt.Sprintf("%02d %s", t.Position, t.Title), t.Format), "")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// CustomerAudioZip - GET /conta/biblioteca/audiobook.zip?livro=N: todas as
// faixas num ZIP só, sem compressão (áudio já é comprimido).
func CustomerAudioZip(w http.ResponseWriter, r *http.Request) {
	customerID := currentCustomerID(r)
	productID, _ := strconv.Atoi(r.URL.Query().Get("livro"))
	if !models.CustomerOwnsAudio(customerID, productID) {
		http.NotFound(w, r)
		return
	}
	key := "download|" + strconv.Itoa(customerID)
	if allowed, _ := downloadLimiter.Allowed(key); !allowed {
		http.Redirect(w, r, "/conta/biblioteca?aviso=limite", http.StatusSeeOther)
		return
	}
	downloadLimiter.RegisterFailure(key)
	p, err := models.GetProduct(productID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	tracks, _ := models.ListAudioTracks(productID)
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(3 * time.Hour))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment",
		map[string]string{"filename": downloadFileName(p.Name+" audiobook", "zip")}))
	w.Header().Set("Cache-Control", "private, no-store")
	zw := zip.NewWriter(w)
	for _, t := range tracks {
		if !models.ValidAudioFileName(t.File) {
			continue
		}
		f, err := os.Open(filepath.Join(audioDir(), t.File))
		if err != nil {
			log.Printf("zip do audiobook %d: %v", productID, err)
			continue
		}
		hdr := &zip.FileHeader{Name: downloadFileName(fmt.Sprintf("%02d %s", t.Position, t.Title), t.Format), Method: zip.Store, Modified: time.Now()}
		dst, err := zw.CreateHeader(hdr)
		if err == nil {
			_, err = io.Copy(dst, f)
		}
		f.Close()
		if err != nil {
			log.Printf("zip do audiobook %d: %v", productID, err)
			return
		}
	}
	zw.Close()
}

// editionOption é uma edição oferecida na página do livro.
type editionOption struct {
	Value, Label, Short, Desc string
	Price                     float64
}

// editionsToBuy lista as edições que o cliente ainda pode comprar.
func editionsToBuy(p *models.Product, ownsEbook, ownsAudio bool) []editionOption {
	ebookDesc := "E-book em " + p.EbookTypeLabel()
	if s := p.EbookSizeLabel(); s != "" {
		ebookDesc += " · " + s
	}
	ebookDesc += " · download logo após o pagamento"
	audioDesc := "Ouça no site ou baixe as faixas"
	if d := p.AudioDurationLabel(); d != "" {
		audioDesc = d + " de narração · ouça no site ou baixe"
	}
	var out []editionOption
	if !ownsEbook {
		out = append(out, editionOption{models.EditionEbook, "E-book", "e-book", ebookDesc, p.Price})
	}
	if p.HasAudio() && !ownsAudio {
		out = append(out, editionOption{models.EditionAudio, "Audiobook", "audiobook", audioDesc, p.AudioPrice})
	}
	if p.HasBundle() && !ownsEbook && !ownsAudio {
		desc := "Os dois juntos"
		if economia := p.Price + p.AudioPrice - p.BundlePrice; economia >= 0.01 {
			desc += " · economize " + models.FormatBRL(economia)
		}
		out = append(out, editionOption{models.EditionCombo, "E-book + audiobook", "e-book + audiobook", desc, p.BundlePrice})
	}
	return out
}

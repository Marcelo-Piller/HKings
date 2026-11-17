package models

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strings"

	"github.com/vendas/loja/database"
)

// Audiobook: a narração de um e-book, enviada pelo vendedor em faixas (uma
// por capítulo, em MP3 ou M4A/M4B). É vendido como edição à parte, com preço
// próprio, e opcionalmente num combo com o e-book:
//
//	ebook  — só o arquivo do e-book (Price)
//	audio  — só o audiobook (AudioPrice)
//	combo  — os dois (BundlePrice)
//
// A posse vem do pedido: order_items.edition diz o que foi comprado. Pedidos
// de e-book anteriores ao audiobook têm edition vazia e valem como "ebook".

const (
	EditionEbook = "ebook"
	EditionAudio = "audio"
	EditionCombo = "combo"

	AudioMP3 = "mp3"
	AudioM4A = "m4a"

	// MaxAudioTrackBytes limita cada faixa; MaxAudioBookBytes, o audiobook
	// inteiro. Um livro de 10 h em MP3 de 64 kbps tem uns 290 MB.
	MaxAudioTrackBytes = 300 << 20
	MaxAudioBookBytes  = 2 << 30
	MaxAudioTracks     = 200

	// AudioSampleSeconds é o tamanho da amostra grátis (início da 1ª faixa).
	AudioSampleSeconds = 300
)

var (
	ErrInvalidAudio   = errors.New("envie um áudio MP3, M4A ou M4B de até 300 MB")
	ErrAudioTooBig    = errors.New("o audiobook passou do limite de 2 GB ou de 200 faixas")
	ErrAudioDuplicate = errors.New("este áudio já está à venda na loja em outro livro")
	ErrAudioNotOwned  = errors.New("audiobook não encontrado na sua biblioteca")
	ErrEditionInvalid = errors.New("esta edição não está à venda")
)

var audioFileName = regexp.MustCompile(`^[0-9a-f]{32}\.(mp3|m4a)$`)

// ValidAudioFileName aceita só nomes gerados pela loja.
func ValidAudioFileName(name string) bool { return audioFileName.MatchString(name) }

// AudioContentType é o tipo enviado ao tocar e baixar.
func AudioContentType(kind string) string {
	if kind == AudioM4A {
		return "audio/mp4"
	}
	return "audio/mpeg"
}

// DetectAudioType reconhece o áudio pelo conteúdo: MP3 começa com a etiqueta
// ID3 ou com um quadro de áudio; M4A/M4B é um MP4 ("ftyp" no 5º byte).
func DetectAudioType(head []byte) (string, error) {
	switch {
	case bytes.HasPrefix(head, []byte("ID3")):
		return AudioMP3, nil
	case len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		return AudioMP3, nil
	case len(head) >= 12 && string(head[4:8]) == "ftyp":
		brand := string(head[8:12])
		switch brand {
		case "M4A ", "M4B ", "mp42", "isom", "mp41", "M4P ", "dash", "iso2", "iso5", "iso6":
			return AudioM4A, nil
		}
	}
	return "", ErrInvalidAudio
}

// ── Duração ──────────────────────────────────────────────────────────────────

// AudioDuration calcula a duração da faixa em segundos (0 se não souber).
func AudioDuration(path, kind string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0
	}
	if kind == AudioM4A {
		return mp4Duration(f, st.Size())
	}
	info, ok := mp3Info(f)
	if !ok {
		return 0
	}
	if info.frames > 0 && info.sampleRate > 0 {
		return int(math.Round(float64(info.frames) * float64(info.samplesPerFrame) / float64(info.sampleRate)))
	}
	if info.bitrate > 0 {
		return int((st.Size() - info.start) * 8 / int64(info.bitrate*1000))
	}
	return 0
}

type mp3Header struct {
	start           int64 // início do primeiro quadro (depois da etiqueta ID3)
	bitrate         int   // kbps
	sampleRate      int
	samplesPerFrame int
	frames          int64 // do cabeçalho Xing/Info, quando houver (VBR)
}

var (
	mp3BitratesV1 = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	mp3BitratesV2 = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
	mp3Rates      = map[int][3]int{3: {44100, 48000, 32000}, 2: {22050, 24000, 16000}, 0: {11025, 12000, 8000}}
)

// mp3Info lê o primeiro quadro MP3 (Layer III).
func mp3Info(r io.ReaderAt) (mp3Header, bool) {
	var h mp3Header
	id3 := make([]byte, 10)
	if _, err := r.ReadAt(id3, 0); err != nil {
		return h, false
	}
	if string(id3[:3]) == "ID3" {
		size := int64(id3[6]&0x7f)<<21 | int64(id3[7]&0x7f)<<14 | int64(id3[8]&0x7f)<<7 | int64(id3[9]&0x7f)
		h.start = 10 + size
		if id3[5]&0x10 != 0 {
			h.start += 10 // rodapé
		}
	}
	buf := make([]byte, 64<<10)
	n, _ := r.ReadAt(buf, h.start)
	buf = buf[:n]
	for i := 0; i+4 <= len(buf); i++ {
		if buf[i] != 0xFF || buf[i+1]&0xE0 != 0xE0 {
			continue
		}
		version := int(buf[i+1]>>3) & 3 // 3 = MPEG1, 2 = MPEG2, 0 = MPEG2.5
		layer := int(buf[i+1]>>1) & 3   // 1 = Layer III
		bitIdx := int(buf[i+2] >> 4)
		rateIdx := int(buf[i+2]>>2) & 3
		if version == 1 || layer != 1 || bitIdx == 0 || bitIdx == 15 || rateIdx == 3 {
			continue
		}
		h.start += int64(i)
		if version == 3 {
			h.bitrate, h.samplesPerFrame = mp3BitratesV1[bitIdx], 1152
		} else {
			h.bitrate, h.samplesPerFrame = mp3BitratesV2[bitIdx], 576
		}
		h.sampleRate = mp3Rates[version][rateIdx]
		// Cabeçalho Xing/Info (VBR): número total de quadros.
		frame := buf[i:]
		for _, tag := range []string{"Xing", "Info"} {
			if j := bytes.Index(frame[:min(len(frame), 200)], []byte(tag)); j >= 0 && j+12 <= len(frame) {
				if binary.BigEndian.Uint32(frame[j+4:])&1 != 0 {
					h.frames = int64(binary.BigEndian.Uint32(frame[j+8:]))
				}
			}
		}
		return h, true
	}
	return h, false
}

// mp4Duration lê a duração do átomo mvhd (dentro de moov).
func mp4Duration(r io.ReaderAt, size int64) int {
	var walk func(start, end int64, path []string) int
	walk = func(start, end int64, path []string) int {
		hdr := make([]byte, 16)
		for pos := start; pos+8 <= end; {
			if _, err := r.ReadAt(hdr[:8], pos); err != nil {
				return 0
			}
			boxSize := int64(binary.BigEndian.Uint32(hdr[:4]))
			kind := string(hdr[4:8])
			headerLen := int64(8)
			if boxSize == 1 {
				if _, err := r.ReadAt(hdr[8:16], pos+8); err != nil {
					return 0
				}
				boxSize = int64(binary.BigEndian.Uint64(hdr[8:16]))
				headerLen = 16
			} else if boxSize == 0 {
				boxSize = end - pos
			}
			if boxSize < headerLen || pos+boxSize > end {
				return 0
			}
			switch kind {
			case "moov":
				return walk(pos+headerLen, pos+boxSize, append(path, kind))
			case "mvhd":
				body := make([]byte, 32)
				if _, err := r.ReadAt(body, pos+headerLen); err != nil {
					return 0
				}
				var scale, dur uint64
				if body[0] == 1 {
					scale, dur = uint64(binary.BigEndian.Uint32(body[20:24])), binary.BigEndian.Uint64(body[24:32])
				} else {
					scale, dur = uint64(binary.BigEndian.Uint32(body[12:16])), uint64(binary.BigEndian.Uint32(body[16:20]))
				}
				if scale == 0 {
					return 0
				}
				return int(dur / scale)
			}
			pos += boxSize
		}
		return 0
	}
	return walk(0, size, nil)
}

// MP3SampleBytes é até onde vai a amostra grátis de uma faixa MP3: o
// arquivo cortado num quadro qualquer continua tocando até ali.
func MP3SampleBytes(path string, seconds int) (int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	st, _ := f.Stat()
	info, ok := mp3Info(f)
	if !ok || info.bitrate == 0 {
		return 0, false
	}
	n := info.start + int64(info.bitrate)*1000/8*int64(seconds)
	if n > st.Size() {
		n = st.Size()
	}
	return n, true
}

// DurationLabel escreve a duração como "7 h 32 min" ou "18 min".
func DurationLabel(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	h, m := seconds/3600, (seconds%3600+59)/60
	if m == 60 {
		h, m = h+1, 0
	}
	switch {
	case h == 0:
		return fmt.Sprintf("%d min", m)
	case m == 0:
		return fmt.Sprintf("%d h", h)
	}
	return fmt.Sprintf("%d h %d min", h, m)
}

// ── Faixas ───────────────────────────────────────────────────────────────────

// AudioTrack é uma faixa (capítulo) do audiobook.
type AudioTrack struct {
	ID          int
	ProductID   int
	Position    int
	Title       string
	File        string
	Format      string
	Size        int64
	DurationSec int
	SHA256      string
}

func (t AudioTrack) DurationLabel() string { return DurationLabel(t.DurationSec) }
func (t AudioTrack) SizeLabel() string     { return fileSizeLabel(t.Size) }

// ListAudioTracks devolve as faixas na ordem de audição.
func ListAudioTracks(productID int) ([]AudioTrack, error) {
	rows, err := database.DB.Query(`SELECT id, product_id, position, title, file, format, size, duration_sec, sha256
		FROM audio_tracks WHERE product_id=? ORDER BY position, id`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AudioTrack
	for rows.Next() {
		var t AudioTrack
		if err := rows.Scan(&t.ID, &t.ProductID, &t.Position, &t.Title, &t.File, &t.Format, &t.Size, &t.DurationSec, &t.SHA256); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AddAudioTrack grava a faixa no fim do audiobook, conferindo os limites e
// que o mesmo arquivo não esteja à venda em outro livro (pirataria).
func AddAudioTrack(t AudioTrack) error {
	if !ValidAudioFileName(t.File) {
		return ErrInvalidAudio
	}
	t.Title = strings.Join(strings.Fields(t.Title), " ")
	if r := []rune(t.Title); len(r) > 120 {
		t.Title = string(r[:120])
	}
	var count int
	var total int64
	database.DB.QueryRow(`SELECT COUNT(*), COALESCE(SUM(size),0) FROM audio_tracks WHERE product_id=?`, t.ProductID).Scan(&count, &total)
	if count >= MaxAudioTracks || total+t.Size > MaxAudioBookBytes {
		return ErrAudioTooBig
	}
	var dup int
	database.DB.QueryRow(`SELECT COUNT(*) FROM audio_tracks WHERE sha256=? AND product_id<>?`, t.SHA256, t.ProductID).Scan(&dup)
	if dup > 0 {
		return ErrAudioDuplicate
	}
	if t.Title == "" {
		t.Title = fmt.Sprintf("Faixa %d", count+1)
	}
	_, err := database.DB.Exec(`INSERT INTO audio_tracks (product_id, position, title, file, format, size, duration_sec, sha256)
		VALUES (?, COALESCE((SELECT MAX(position) FROM audio_tracks WHERE product_id=?), 0) + 1, ?,?,?,?,?,?)`,
		t.ProductID, t.ProductID, t.Title, t.File, t.Format, t.Size, t.DurationSec, t.SHA256)
	return err
}

// DeleteAudioTrack apaga a faixa e devolve o arquivo, para quem chama
// remover do disco.
func DeleteAudioTrack(productID, trackID int) (string, error) {
	var file string
	if err := database.DB.QueryRow(`SELECT file FROM audio_tracks WHERE id=? AND product_id=?`, trackID, productID).Scan(&file); err != nil {
		return "", err
	}
	_, err := database.DB.Exec(`DELETE FROM audio_tracks WHERE id=? AND product_id=?`, trackID, productID)
	return file, err
}

// MoveAudioTrack troca a faixa de lugar com a anterior (delta -1) ou a
// seguinte (+1).
func MoveAudioTrack(productID, trackID, delta int) error {
	tracks, err := ListAudioTracks(productID)
	if err != nil {
		return err
	}
	for i, t := range tracks {
		if t.ID != trackID {
			continue
		}
		j := i + delta
		if j < 0 || j >= len(tracks) {
			return nil
		}
		tracks[i], tracks[j] = tracks[j], tracks[i]
		tx, err := database.DB.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for k, tr := range tracks {
			if _, err := tx.Exec(`UPDATE audio_tracks SET position=? WHERE id=?`, k+1, tr.ID); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	return sql.ErrNoRows
}

// RenameAudioTrack muda o título da faixa.
func RenameAudioTrack(productID, trackID int, title string) error {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" || len([]rune(title)) > 120 {
		return ErrInvalidAudio
	}
	_, err := database.DB.Exec(`UPDATE audio_tracks SET title=? WHERE id=? AND product_id=?`, title, trackID, productID)
	return err
}

// AudioFileInUse diz se alguma faixa ainda aponta para o arquivo.
func AudioFileInUse(name string) bool {
	var n int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM audio_tracks WHERE file=?`, name).Scan(&n); err != nil {
		return true
	}
	return n > 0
}

// SetAudioPrices grava os preços do audiobook e do combo. Audiobook com
// preço zero não está à venda; combo com preço zero não é oferecido.
func SetAudioPrices(productID int, audio, bundle float64, rightsDeclared bool) error {
	for _, v := range []float64{audio, bundle} {
		if v < 0 || v > 1_000_000 || math.IsNaN(v) {
			return fmt.Errorf("%w: preço inválido", ErrInvalidProduct)
		}
	}
	audio, bundle = math.Round(audio*100)/100, math.Round(bundle*100)/100
	if bundle > 0 && audio == 0 {
		return fmt.Errorf("%w: para oferecer o combo, informe também o preço do audiobook", ErrInvalidProduct)
	}
	_, err := database.DB.Exec(`UPDATE products SET audio_price=?, bundle_price=?,
		audio_rights_at = CASE WHEN ? THEN CURRENT_TIMESTAMP ELSE audio_rights_at END
		WHERE id=? AND product_format='digital'`, audio, bundle, rightsDeclared, productID)
	return err
}

// AudioRightsDeclared diz se o vendedor declarou ter os direitos do áudio.
func AudioRightsDeclared(productID int) bool {
	var at string
	database.DB.QueryRow(`SELECT COALESCE(audio_rights_at,'') FROM products WHERE id=?`, productID).Scan(&at)
	return at != ""
}

// ── Edições do livro ─────────────────────────────────────────────────────────

// HasAudio diz se o audiobook está à venda.
func (p Product) HasAudio() bool { return p.IsDigital() && p.AudioTracks > 0 && p.AudioPrice > 0 }

// HasBundle diz se o combo e-book + audiobook está à venda.
func (p Product) HasBundle() bool { return p.HasAudio() && p.BundlePrice > 0 }

// AudioDurationLabel é a duração total do audiobook.
func (p Product) AudioDurationLabel() string { return DurationLabel(p.AudioSeconds) }

// ValidEdition normaliza a edição pedida para este livro; livro físico não
// tem edição. Devolve "" e false quando a edição não está à venda.
func (p Product) ValidEdition(e string) (string, bool) {
	if !p.IsDigital() {
		return "", true
	}
	switch e {
	case "", EditionEbook:
		return EditionEbook, true
	case EditionAudio:
		return EditionAudio, p.HasAudio()
	case EditionCombo:
		return EditionCombo, p.HasBundle()
	}
	return "", false
}

// PriceFor é o preço da edição.
func (p Product) PriceFor(edition string) float64 {
	switch edition {
	case EditionAudio:
		return p.AudioPrice
	case EditionCombo:
		return p.BundlePrice
	}
	return p.Price
}

// EditionLabel descreve a edição para a sacola, o pedido e o comprovante.
func EditionLabel(edition string) string {
	switch edition {
	case EditionAudio:
		return "Audiobook"
	case EditionCombo:
		return "E-book + audiobook"
	case EditionEbook:
		return "E-book"
	}
	return ""
}

// editionIncludes diz se a edição dá acesso ao e-book ou ao áudio.
func editionIncludes(edition, what string) bool {
	switch edition {
	case EditionCombo:
		return true
	case EditionAudio:
		return what == EditionAudio
	default: // "" (pedidos antigos) e "ebook"
		return what == EditionEbook
	}
}

// customerOwns diz se o cliente tem pedido pago que dá acesso ao e-book
// (what = EditionEbook) ou ao audiobook (EditionAudio).
func customerOwns(customerID, productID int, what string) bool {
	if customerID <= 0 || productID <= 0 {
		return false
	}
	rows, err := database.DB.Query(`SELECT oi.edition FROM order_items oi JOIN orders o ON o.id = oi.order_id
		WHERE o.customer_id=? AND oi.product_id=? AND o.status IN `+paidStatuses, customerID, productID)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var e string
		if rows.Scan(&e) == nil && editionIncludes(e, what) {
			return true
		}
	}
	return false
}

// CustomerOwnsAudio diz se o cliente comprou o audiobook (sozinho ou no combo).
func CustomerOwnsAudio(customerID, productID int) bool {
	return customerOwns(customerID, productID, EditionAudio)
}

// CustomerOwnsEdition diz se comprar esta edição repetiria algo que o
// cliente já tem: e-book já comprado barra "ebook" e "combo"; audiobook já
// comprado barra "audio" e "combo".
func CustomerOwnsEdition(customerID, productID int, edition string) bool {
	ebook, audio := CustomerOwnsProduct(customerID, productID), CustomerOwnsAudio(customerID, productID)
	switch edition {
	case EditionAudio:
		return audio
	case EditionCombo:
		return ebook || audio
	}
	return ebook
}

// GetAudioTrackForCustomer confere a posse e devolve a faixa.
func GetAudioTrackForCustomer(customerID, productID, trackID int) (*AudioTrack, error) {
	if !CustomerOwnsAudio(customerID, productID) {
		return nil, ErrAudioNotOwned
	}
	tracks, err := ListAudioTracks(productID)
	if err != nil {
		return nil, err
	}
	for _, t := range tracks {
		if t.ID == trackID && ValidAudioFileName(t.File) {
			return &t, nil
		}
	}
	return nil, ErrAudioNotOwned
}

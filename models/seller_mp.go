package models

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vendas/loja/database"
)

// Conta Mercado Pago de um vendedor, conectada pelo OAuth. Os tokens dão
// acesso à conta dele, então ficam criptografados no banco e nunca vão para
// template nenhum.

var ErrSellerNotConnected = errors.New("vendedor sem conta Mercado Pago conectada")

type SellerMPAccount struct {
	SellerID     int
	UserID       string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// SaveSellerMPTokens grava (ou substitui) a conexão do vendedor.
func SaveSellerMPTokens(sellerID int, userID, accessToken, refreshToken string, expiresAt time.Time) error {
	if sellerID <= 0 || strings.TrimSpace(userID) == "" || accessToken == "" {
		return fmt.Errorf("dados de conexão incompletos")
	}
	access, err := encryptSetting(accessToken)
	if err != nil {
		return err
	}
	refresh := ""
	if refreshToken != "" {
		if refresh, err = encryptSetting(refreshToken); err != nil {
			return err
		}
	}
	_, err = database.DB.Exec(
		`UPDATE sellers SET mp_user_id=?, mp_access_token=?, mp_refresh_token=?, mp_token_expires_at=? WHERE id=?`,
		userID, access, refresh, expiresAt.UTC().Format(time.RFC3339), sellerID,
	)
	return err
}

// GetSellerMPAccount devolve a conexão já descriptografada.
func GetSellerMPAccount(sellerID int) (*SellerMPAccount, error) {
	var userID, access, refresh string
	var expires sql.NullString
	err := database.DB.QueryRow(
		`SELECT mp_user_id, mp_access_token, mp_refresh_token, mp_token_expires_at FROM sellers WHERE id=? AND active=1`,
		sellerID,
	).Scan(&userID, &access, &refresh, &expires)
	if err != nil {
		return nil, err
	}
	if userID == "" || access == "" {
		return nil, ErrSellerNotConnected
	}
	account := &SellerMPAccount{SellerID: sellerID, UserID: userID}
	if account.AccessToken, err = decryptSetting(access); err != nil {
		return nil, fmt.Errorf("token do vendedor ilegível (SETTINGS_ENCRYPTION_KEY mudou?): %w", err)
	}
	if refresh != "" {
		if account.RefreshToken, err = decryptSetting(refresh); err != nil {
			return nil, fmt.Errorf("refresh token do vendedor ilegível: %w", err)
		}
	}
	if expires.Valid {
		account.ExpiresAt, _ = time.Parse(time.RFC3339, expires.String)
	}
	return account, nil
}

// DisconnectSellerMP apaga a conexão. Os livros do vendedor saem da vitrine
// até ele conectar de novo, porque sem conta não há para onde mandar o dinheiro.
func DisconnectSellerMP(sellerID int) error {
	_, err := database.DB.Exec(
		`UPDATE sellers SET mp_user_id='', mp_access_token='', mp_refresh_token='', mp_token_expires_at=NULL WHERE id=?`,
		sellerID,
	)
	return err
}

// SellerIDByMPUser encontra o vendedor dono de uma conta Mercado Pago, para
// saber com qual token consultar um pagamento avisado pelo webhook.
func SellerIDByMPUser(userID string) (int, error) {
	if _, err := strconv.ParseInt(userID, 10, 64); err != nil {
		return 0, ErrSellerNotConnected
	}
	var id int
	err := database.DB.QueryRow(`SELECT id FROM sellers WHERE mp_user_id=? AND mp_access_token<>'' LIMIT 1`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrSellerNotConnected
	}
	return id, err
}

// SellerCanReceive informa se o vendedor está pronto para vender. O site
// (sellerID 0) sempre pode.
func SellerCanReceive(sellerID int) bool {
	if sellerID == 0 {
		return true
	}
	var userID string
	err := database.DB.QueryRow(`SELECT mp_user_id FROM sellers WHERE id=? AND active=1 AND mp_access_token<>''`, sellerID).Scan(&userID)
	return err == nil && userID != ""
}

// MarketplaceFeePercent é a comissão do site sobre vendas de vendedores.
// Padrão de 10%; o admin pode mudar em Configurações, entre 0 e 50%.
func MarketplaceFeePercent() float64 {
	const padrao = 10.0
	raw := strings.TrimSpace(strings.ReplaceAll(GetSetting("comissao_percentual"), ",", "."))
	if raw == "" {
		return padrao
	}
	pct, err := strconv.ParseFloat(raw, 64)
	if err != nil || pct < 0 || pct > 50 {
		return padrao
	}
	return pct
}

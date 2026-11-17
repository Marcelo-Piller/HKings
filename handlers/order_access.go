package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/vendas/loja/models"
)

const guestOrderIDsKey = "guest_order_ids"

// rememberGuestOrder vincula ao cookie da compra um número limitado de pedidos
// anônimos. Assim, IDs sequenciais não servem como credencial para ler dados
// pessoais nem códigos de pagamento de outros compradores.
func rememberGuestOrder(w http.ResponseWriter, r *http.Request, orderID int) {
	if currentCustomerID(r) != 0 || orderID <= 0 {
		return
	}
	sess, err := store.Get(r, "session")
	if err != nil {
		return
	}
	ids := guestOrderIDs(sess.Values[guestOrderIDsKey])
	for _, id := range ids {
		if id == orderID {
			return
		}
	}
	ids = append(ids, orderID)
	if len(ids) > 20 {
		ids = ids[len(ids)-20:]
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return
	}
	sess.Values[guestOrderIDsKey] = encoded
	_ = sess.Save(r, w)
}

func guestOrderIDs(raw any) []int {
	encoded, ok := raw.([]byte)
	if !ok {
		return nil
	}
	var ids []int
	if json.Unmarshal(encoded, &ids) != nil {
		return nil
	}
	return ids
}

func canAccessOrder(r *http.Request, order *models.Order) bool {
	if order == nil {
		return false
	}
	if isAdminLoggedIn(r) {
		return true
	}
	if customerID := currentCustomerID(r); customerID > 0 {
		return order.CustomerID == customerID
	}
	sess, err := store.Get(r, "session")
	if err != nil {
		return false
	}
	for _, id := range guestOrderIDs(sess.Values[guestOrderIDsKey]) {
		if id == order.ID {
			return true
		}
	}
	return false
}

func requireOrderAccess(w http.ResponseWriter, r *http.Request, order *models.Order) bool {
	if canAccessOrder(r, order) {
		return true
	}
	http.NotFound(w, r)
	return false
}

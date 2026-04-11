package api

import (
	"encoding/json"
	"net/http"

	"moleAgent_Serv/internal/core"
)

func ResponseOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(core.ApiResponse{Code: 0, Msg: "success", Data: data})
}

func ResponseError(w http.ResponseWriter, httpStatus, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(core.ApiResponse{Code: code, Msg: msg})
}

func ResponsePaginated(w http.ResponseWriter, items any, total, page, perPage int) {
	totalPages := (total + perPage - 1) / perPage
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(core.ApiResponse{
		Code: 0,
		Msg:  "success",
		Data: core.PaginatedResponse{
			Items:      items,
			Total:      total,
			Page:       page,
			PerPage:    perPage,
			TotalPages: totalPages,
		},
	})
}

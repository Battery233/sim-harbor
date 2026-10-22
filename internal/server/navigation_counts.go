package server

import "net/http"

func (s *Server) navigationCounts(w http.ResponseWriter, r *http.Request) {
	counts, err := s.Store.NavigationCounts(r.Context(), s.Config.DataDir)
	if err != nil {
		outgoingError(w, err)
		return
	}
	reply(w, 200, counts)
}

package api

import (
	"encoding/json"
	"net/http"
)

// getProxy returns the caller's effective tracker proxy, the server default,
// and the last probe result.
//
//	@Summary	Get my tracker proxy
//	@Tags		proxy
//	@Produce	json
//	@Success	200	{object}	proxyInfo
//	@Router		/api/proxy [get]
func (s *server) getProxy(w http.ResponseWriter, r *http.Request) {
	s.writeProxyInfo(w, s.uploader(r))
}

// setProxy sets the caller's own tracker proxy, then probes it. A malformed or
// local/private address is rejected; an unreachable but well-formed proxy is
// saved and reported with status "error".
//
//	@Summary	Set my tracker proxy
//	@Tags		proxy
//	@Accept		json
//	@Param		body	body	proxyUpdate	true	"Proxy URL (empty = direct)"
//	@Success	200	{object}	proxyInfo
//	@Failure	400	{object}	errorResponse
//	@Router		/api/proxy [put]
func (s *server) setProxy(w http.ResponseWriter, r *http.Request) {
	s.applyProxy(w, r, s.uploader(r))
}

// writeProxyInfo responds with owner's effective proxy, the server default, and
// the last probe result. Shared by the self and admin GET handlers.
func (s *server) writeProxyInfo(w http.ResponseWriter, owner string) {
	px, _ := s.mgr.UserProxy(owner)
	status, errMsg := s.mgr.ProxyStatus(owner)
	writeJSON(w, http.StatusOK, proxyInfo{Proxy: px, Default: s.mgr.ProxyDefault(), Status: status, Error: errMsg})
}

// applyProxy decodes a proxyUpdate body, stores it for owner, probes the result,
// and responds with the resulting state. A malformed or local/private address
// is rejected; an unreachable but well-formed proxy is saved and reported with
// status "error". Shared by the self and admin PUT handlers.
func (s *server) applyProxy(w http.ResponseWriter, r *http.Request, owner string) {
	var body proxyUpdate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.mgr.SetUserProxy(owner, body.Proxy); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mgr.ProbeUserProxy(r.Context(), owner)
	s.writeProxyInfo(w, owner)
}

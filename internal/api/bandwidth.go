package api

import (
	"encoding/json"
	"net/http"

	"github.com/jdel/emission/internal/units"
)

// getBandwidth returns the caller's own upload-bandwidth ceiling and the server
// default. The owner key is the caller's storage scope (their username, or the
// root "" bucket when auth is disabled).
//
//	@Summary	Get my upload bandwidth
//	@Tags		bandwidth
//	@Produce	json
//	@Success	200	{object}	bandwidthInfo
//	@Router		/api/bandwidth [get]
func (s *server) getBandwidth(w http.ResponseWriter, r *http.Request) {
	owner := s.uploader(r)
	writeJSON(w, http.StatusOK, bandwidthInfo{
		Bandwidth:      s.mgr.Bandwidth(owner),
		Default:        s.mgr.DefaultBandwidth(),
		Profile:        s.mgr.Profile(owner),
		HalfSaturation: s.mgr.HalfSaturation(owner),
	})
}

// setMyBandwidth sets the caller's own upload-bandwidth ceiling.
//
//	@Summary	Set my upload bandwidth
//	@Tags		bandwidth
//	@Accept		json
//	@Param		body	body	bandwidthUpdate	true	"Bandwidth (e.g. 2M)"
//	@Success	204
//	@Failure	400	{object}	errorResponse
//	@Router		/api/bandwidth [put]
func (s *server) setMyBandwidth(w http.ResponseWriter, r *http.Request) {
	s.applyBandwidth(w, r, s.uploader(r))
}

// applyBandwidth decodes a bandwidthUpdate body and stores it for owner.
func (s *server) applyBandwidth(w http.ResponseWriter, r *http.Request, owner string) {
	var body bandwidthUpdate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	v, err := units.ParseRate(body.Bandwidth)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bandwidth: "+err.Error())
		return
	}
	if err := s.mgr.SetBandwidth(owner, v); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.HalfSaturation != nil {
		if err := s.mgr.SetHalfSaturation(owner, *body.HalfSaturation); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

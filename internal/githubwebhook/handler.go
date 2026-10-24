package githubwebhook

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	maxBodyBytes = 5 * 1024 * 1024
)

type Handler struct {
	// secret is the webhook secret configured in GitHub, used to verify every delivery.
	secret  []byte
	service *service
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := logf.FromContext(r.Context()).WithValues("delivery", r.Header.Get("X-GitHub-Delivery"))

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	// Verified before anything else reads the body: until then it is untrusted input.
	if err := verifySignature(h.secret, body, r.Header.Get(signatureHeader)); err != nil {
		log.Info("Rejected webhook delivery", "reason", err.Error())
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	// Other events have a different shape, so only pull_request payloads are decoded.
	switch r.Header.Get("X-GitHub-Event") {
	case "ping":
		w.WriteHeader(http.StatusOK)
		return
	case "pull_request":
	default:
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var event pullRequestEvent
	if err := json.Unmarshal(body, &event); err != nil {
		log.Error(err, "Failed to decode pull_request event")
		http.Error(w, "malformed pull_request payload", http.StatusBadRequest)
		return
	}
	if err := event.validate(); err != nil {
		http.Error(w, "invalid pull_request payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	switch event.Action {
	case actionOpened, actionReopened, actionSynchronize:
		var p preview
		p, err = h.service.previewFor(r.Context(), event)
		if err == nil {
			err = h.service.upsert(r.Context(), p)
		}
	case actionClosed:
		err = h.service.delete(r.Context(), event.Repository.FullName, event.Number)
	default:
		w.WriteHeader(http.StatusNoContent)
		return
	}

	switch {
	case err == nil:
		w.WriteHeader(http.StatusAccepted)
	case errors.Is(err, errNoPreviewConfig):
		// The repository hasn't opted into previews.
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, errInvalidPreviewConfig):
		// Only describes the repository's own file, and helps whoever reads the delivery in GitHub.
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	default:
		log.Error(err, "Failed to apply pull_request event", "action", event.Action)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

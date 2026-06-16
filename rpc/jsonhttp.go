package rpc

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

type jsonHTTPErrorResponse struct {
	Error *Error `json:"error"`
}

type jsonHTTPTransferRequest struct {
	SourceSeed      string `json:"source_seed,omitempty"`
	SourceSeedCamel string `json:"sourceSeed,omitempty"`
	Destination     string `json:"destination,omitempty"`
	Lamports        uint64 `json:"lamports,omitempty"`
}

type jsonHTTPTransactionRequest struct {
	Transaction        string `json:"transaction,omitempty"`
	EncodedTransaction string `json:"encodedTransaction,omitempty"`
}

type jsonHTTPValidatorRegisterRequest struct {
	StakerSeed         string `json:"staker_seed,omitempty"`
	StakerSeedCamel    string `json:"stakerSeed,omitempty"`
	ValidatorSeed      string `json:"validator_seed,omitempty"`
	ValidatorSeedCamel string `json:"validatorSeed,omitempty"`
	ConsensusSeed      string `json:"consensus_seed,omitempty"`
	ConsensusSeedCamel string `json:"consensusSeed,omitempty"`
	PeerID             string `json:"peer_id,omitempty"`
	PeerIDCamel        string `json:"peerID,omitempty"`
	StakeLamports      uint64 `json:"stake_lamports,omitempty"`
	StakeLamportsCamel uint64 `json:"stakeLamports,omitempty"`
}

type jsonHTTPStakeRequest struct {
	StakerSeed            string `json:"staker_seed,omitempty"`
	StakerSeedCamel       string `json:"stakerSeed,omitempty"`
	ValidatorAddress      string `json:"validator_address,omitempty"`
	ValidatorAddressCamel string `json:"validatorAddress,omitempty"`
	Lamports              uint64 `json:"lamports,omitempty"`
}

type jsonHTTPUnstakeRequest struct {
	StakerSeed            string `json:"staker_seed,omitempty"`
	StakerSeedCamel       string `json:"stakerSeed,omitempty"`
	ValidatorAddress      string `json:"validator_address,omitempty"`
	ValidatorAddressCamel string `json:"validatorAddress,omitempty"`
	Lamports              uint64 `json:"lamports,omitempty"`
	UnlockEpoch           uint64 `json:"unlock_epoch,omitempty"`
	UnlockEpochCamel      uint64 `json:"unlockEpoch,omitempty"`
}

type jsonHTTPJailRequest struct {
	StakerSeed            string `json:"staker_seed,omitempty"`
	StakerSeedCamel       string `json:"stakerSeed,omitempty"`
	ValidatorAddress      string `json:"validator_address,omitempty"`
	ValidatorAddressCamel string `json:"validatorAddress,omitempty"`
	JailUntilEpoch        uint64 `json:"jail_until_epoch,omitempty"`
	JailUntilEpochCamel   uint64 `json:"jailUntilEpoch,omitempty"`
}

func (s *Server) serveJSONHTTP(w *statusResponseWriter, r *http.Request) {
	method, params, rpcError, statusCode := s.jsonHTTPRoute(r)
	if rpcError != nil {
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(jsonHTTPErrorResponse{Error: rpcError})
		return
	}

	response := s.router.Handle(r.Context(), Request{
		JSONRPC: jsonRPCVersion,
		ID:      json.RawMessage("null"),
		Method:  method,
		Params:  params,
	})
	if response.Error != nil {
		w.WriteHeader(jsonHTTPStatusCode(response.Error))
		_ = json.NewEncoder(w).Encode(jsonHTTPErrorResponse{Error: response.Error})
		return
	}
	_ = json.NewEncoder(w).Encode(response.Result)
}

func (s *Server) jsonHTTPRoute(r *http.Request) (string, json.RawMessage, *Error, int) {
	path := strings.TrimRight(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}
	switch {
	case r.Method == http.MethodGet && path == "/health":
		return MethodGetHealth, emptyParams(), nil, http.StatusOK
	case r.Method == http.MethodGet && path == "/metrics":
		return MethodGetMetrics, emptyParams(), nil, http.StatusOK
	case r.Method == http.MethodGet && path == "/node/status":
		return MethodGetNodeStatus, emptyParams(), nil, http.StatusOK
	case r.Method == http.MethodGet && path == "/consensus/status":
		return MethodGetConsensusStatus, emptyParams(), nil, http.StatusOK
	case r.Method == http.MethodGet && path == "/validators":
		return MethodGetValidatorSet, emptyParams(), nil, http.StatusOK
	case r.Method == http.MethodGet && path == "/balance":
		return routeGetBalance(r)
	case r.Method == http.MethodGet && path == "/block":
		return routeGetBlockFromQuery(r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/blocks/"):
		return routeGetBlockFromPath(path)
	case r.Method == http.MethodPost && path == "/transactions/send":
		return s.routeSendTransaction(r)
	case r.Method == http.MethodPost && path == "/treasury/transfer":
		return s.routeTreasuryTransfer(r)
	case r.Method == http.MethodPost && path == "/transfer":
		return s.routeTransfer(r)
	case r.Method == http.MethodPost && path == "/validators/register":
		return s.routeRegisterValidator(r)
	case r.Method == http.MethodPost && path == "/stake":
		return s.routeStake(r)
	case r.Method == http.MethodPost && path == "/unstake":
		return s.routeUnstake(r)
	case r.Method == http.MethodPost && path == "/validators/slash":
		return s.routeSlashValidator(r)
	case r.Method == http.MethodPost && path == "/validators/jail":
		return s.routeJailValidator(r)
	case path == "/health" || path == "/metrics" || path == "/node/status" || path == "/consensus/status" ||
		path == "/validators" || path == "/balance" || path == "/block" || strings.HasPrefix(path, "/blocks/") ||
		path == "/transactions/send" || path == "/treasury/transfer" || path == "/transfer" ||
		path == "/validators/register" || path == "/stake" || path == "/unstake" ||
		path == "/validators/slash" || path == "/validators/jail":
		return "", nil, ErrInvalidRequest, http.StatusMethodNotAllowed
	default:
		return "", nil, methodNotFoundError(path), http.StatusNotFound
	}
}

func routeGetBalance(r *http.Request) (string, json.RawMessage, *Error, int) {
	address := strings.TrimSpace(r.URL.Query().Get("address"))
	if address == "" {
		return "", nil, invalidParamsError("balance address is required"), http.StatusBadRequest
	}
	return MethodGetBalance, paramsArray(address), nil, http.StatusOK
}

func routeGetBlockFromQuery(r *http.Request) (string, json.RawMessage, *Error, int) {
	slot, err := parsePositiveUint64(r.URL.Query().Get("slot"), "block slot")
	if err != nil {
		return "", nil, err, http.StatusBadRequest
	}
	return MethodGetBlock, paramsArray(slot), nil, http.StatusOK
}

func routeGetBlockFromPath(path string) (string, json.RawMessage, *Error, int) {
	value := strings.TrimPrefix(path, "/blocks/")
	slot, err := parsePositiveUint64(value, "block slot")
	if err != nil {
		return "", nil, err, http.StatusBadRequest
	}
	return MethodGetBlock, paramsArray(slot), nil, http.StatusOK
}

func (s *Server) routeSendTransaction(r *http.Request) (string, json.RawMessage, *Error, int) {
	request := jsonHTTPTransactionRequest{}
	if rpcError, statusCode := s.decodeJSONHTTPRequest(r, &request); rpcError != nil {
		return "", nil, rpcError, statusCode
	}
	encodedTransaction := firstNonEmpty(request.Transaction, request.EncodedTransaction)
	if encodedTransaction == "" {
		return "", nil, invalidParamsError("transaction is required"), http.StatusBadRequest
	}
	return MethodSendTransaction, paramsArray(encodedTransaction), nil, http.StatusOK
}

func (s *Server) routeTreasuryTransfer(r *http.Request) (string, json.RawMessage, *Error, int) {
	request := jsonHTTPTransferRequest{}
	if rpcError, statusCode := s.decodeJSONHTTPRequest(r, &request); rpcError != nil {
		return "", nil, rpcError, statusCode
	}
	if request.Destination == "" || request.Lamports == 0 {
		return "", nil, invalidParamsError("destination and lamports are required"), http.StatusBadRequest
	}
	return MethodTreasuryTransfer, paramsArray(request.Destination, request.Lamports), nil, http.StatusOK
}

func (s *Server) routeTransfer(r *http.Request) (string, json.RawMessage, *Error, int) {
	request := jsonHTTPTransferRequest{}
	if rpcError, statusCode := s.decodeJSONHTTPRequest(r, &request); rpcError != nil {
		return "", nil, rpcError, statusCode
	}
	sourceSeed := firstNonEmpty(request.SourceSeed, request.SourceSeedCamel)
	if sourceSeed == "" || request.Destination == "" || request.Lamports == 0 {
		return "", nil, invalidParamsError("source seed, destination and lamports are required"), http.StatusBadRequest
	}
	return MethodTransfer, paramsArray(sourceSeed, request.Destination, request.Lamports), nil, http.StatusOK
}

func (s *Server) routeRegisterValidator(r *http.Request) (string, json.RawMessage, *Error, int) {
	request := jsonHTTPValidatorRegisterRequest{}
	if rpcError, statusCode := s.decodeJSONHTTPRequest(r, &request); rpcError != nil {
		return "", nil, rpcError, statusCode
	}
	stakerSeed := firstNonEmpty(request.StakerSeed, request.StakerSeedCamel)
	validatorSeed := firstNonEmpty(request.ValidatorSeed, request.ValidatorSeedCamel)
	consensusSeed := firstNonEmpty(request.ConsensusSeed, request.ConsensusSeedCamel)
	peerID := firstNonEmpty(request.PeerID, request.PeerIDCamel)
	stakeLamports := firstPositive(request.StakeLamports, request.StakeLamportsCamel)
	if stakerSeed == "" || validatorSeed == "" || consensusSeed == "" || peerID == "" || stakeLamports == 0 {
		return "", nil, invalidParamsError("staker seed, validator seed, consensus seed, peer id and stake lamports are required"), http.StatusBadRequest
	}
	return MethodRegisterValidator, paramsArray(stakerSeed, validatorSeed, consensusSeed, peerID, stakeLamports), nil, http.StatusOK
}

func (s *Server) routeStake(r *http.Request) (string, json.RawMessage, *Error, int) {
	request := jsonHTTPStakeRequest{}
	if rpcError, statusCode := s.decodeJSONHTTPRequest(r, &request); rpcError != nil {
		return "", nil, rpcError, statusCode
	}
	stakerSeed := firstNonEmpty(request.StakerSeed, request.StakerSeedCamel)
	validatorAddress := firstNonEmpty(request.ValidatorAddress, request.ValidatorAddressCamel)
	if stakerSeed == "" || validatorAddress == "" || request.Lamports == 0 {
		return "", nil, invalidParamsError("staker seed, validator address and lamports are required"), http.StatusBadRequest
	}
	return MethodStake, paramsArray(stakerSeed, validatorAddress, request.Lamports), nil, http.StatusOK
}

func (s *Server) routeUnstake(r *http.Request) (string, json.RawMessage, *Error, int) {
	request := jsonHTTPUnstakeRequest{}
	if rpcError, statusCode := s.decodeJSONHTTPRequest(r, &request); rpcError != nil {
		return "", nil, rpcError, statusCode
	}
	stakerSeed := firstNonEmpty(request.StakerSeed, request.StakerSeedCamel)
	validatorAddress := firstNonEmpty(request.ValidatorAddress, request.ValidatorAddressCamel)
	unlockEpoch := firstPositive(request.UnlockEpoch, request.UnlockEpochCamel)
	if stakerSeed == "" || validatorAddress == "" || request.Lamports == 0 || unlockEpoch == 0 {
		return "", nil, invalidParamsError("staker seed, validator address, lamports and unlock epoch are required"), http.StatusBadRequest
	}
	return MethodUnstake, paramsArray(stakerSeed, validatorAddress, request.Lamports, unlockEpoch), nil, http.StatusOK
}

func (s *Server) routeSlashValidator(r *http.Request) (string, json.RawMessage, *Error, int) {
	request := jsonHTTPStakeRequest{}
	if rpcError, statusCode := s.decodeJSONHTTPRequest(r, &request); rpcError != nil {
		return "", nil, rpcError, statusCode
	}
	stakerSeed := firstNonEmpty(request.StakerSeed, request.StakerSeedCamel)
	validatorAddress := firstNonEmpty(request.ValidatorAddress, request.ValidatorAddressCamel)
	if stakerSeed == "" || validatorAddress == "" || request.Lamports == 0 {
		return "", nil, invalidParamsError("staker seed, validator address and lamports are required"), http.StatusBadRequest
	}
	return MethodSlashValidator, paramsArray(stakerSeed, validatorAddress, request.Lamports), nil, http.StatusOK
}

func (s *Server) routeJailValidator(r *http.Request) (string, json.RawMessage, *Error, int) {
	request := jsonHTTPJailRequest{}
	if rpcError, statusCode := s.decodeJSONHTTPRequest(r, &request); rpcError != nil {
		return "", nil, rpcError, statusCode
	}
	stakerSeed := firstNonEmpty(request.StakerSeed, request.StakerSeedCamel)
	validatorAddress := firstNonEmpty(request.ValidatorAddress, request.ValidatorAddressCamel)
	jailUntilEpoch := firstPositive(request.JailUntilEpoch, request.JailUntilEpochCamel)
	if stakerSeed == "" || validatorAddress == "" || jailUntilEpoch == 0 {
		return "", nil, invalidParamsError("staker seed, validator address and jail until epoch are required"), http.StatusBadRequest
	}
	return MethodJailValidator, paramsArray(stakerSeed, validatorAddress, jailUntilEpoch), nil, http.StatusOK
}

func (s *Server) decodeJSONHTTPRequest(r *http.Request, value any) (*Error, int) {
	body, err := readRequestBody(nil, r, s.maxBodyBytes)
	if err != nil {
		return ErrRequestBodyTooLarge, http.StatusRequestEntityTooLarge
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return invalidParamsError("json request body is required"), http.StatusBadRequest
	}
	if err := decodeStrict(body, value); err != nil {
		return errorWithData(ErrParseError, err.Error()), http.StatusBadRequest
	}
	return nil, http.StatusOK
}

func emptyParams() json.RawMessage {
	return json.RawMessage("[]")
}

func paramsArray(values ...any) json.RawMessage {
	encoded, err := json.Marshal(values)
	if err != nil {
		return emptyParams()
	}
	return encoded
}

func parsePositiveUint64(value string, field string) (uint64, *Error) {
	number, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil || number == 0 {
		return 0, invalidParamsError(field + " must be a positive unsigned integer")
	}
	return number, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstPositive(values ...uint64) uint64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func jsonHTTPStatusCode(rpcError *Error) int {
	if rpcError == nil {
		return http.StatusOK
	}
	switch rpcError.Code {
	case CodeParseError, CodeInvalidParams, CodeInvalidRequest:
		return http.StatusBadRequest
	case CodeMethodNotFound:
		return http.StatusNotFound
	case CodeMethodUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

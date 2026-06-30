package posnode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"solana_golang/rpc"
)

func TestPrepareBootstrapJoinAppliesFrozenManifestForKnownValidator(t *testing.T) {
	coordinatorConfig := testBootstrapCoordinatorConfig(t, 1)
	coordinator, err := newBootstrapCoordinator(coordinatorConfig, testBootstrapLogger())
	if err != nil {
		t.Fatalf("newBootstrapCoordinator() error = %v", err)
	}
	registration := testBootstrapRegistration(t, coordinatorConfig, 1)
	if _, err := coordinator.BootstrapRegisterValidator(context.Background(), registration); err != nil {
		t.Fatalf("BootstrapRegisterValidator() error = %v", err)
	}
	manifest, err := coordinator.GetBootstrapManifest(context.Background())
	if err != nil {
		t.Fatalf("GetBootstrapManifest() error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		decoded := bootstrapRPCRequest{}
		if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil {
			t.Fatalf("Decode(request) error = %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch decoded.Method {
		case rpc.MethodBootstrapRegisterValidator:
			_ = json.NewEncoder(writer).Encode(bootstrapRPCResponse{
				JSONRPC: bootstrapRPCVersion,
				ID:      decoded.ID,
				Error: &rpc.Error{
					Code:    -32603,
					Message: "internal error",
					Data:    "bootstrap register validator: posnode: bootstrap manifest already frozen",
				},
			})
		case rpc.MethodGetBootstrapManifest:
			_ = json.NewEncoder(writer).Encode(bootstrapRPCResponse{
				JSONRPC: bootstrapRPCVersion,
				ID:      decoded.ID,
				Result:  mustMarshalRawMessage(t, manifest),
			})
		default:
			t.Fatalf("unexpected method %q", decoded.Method)
		}
	}))
	defer server.Close()

	config := minimalNodeConfigForValidation()
	config.NodeName = "validator-1"
	config.PeerSeed = "bootstrap-validator-peer-1"
	config.StakerSeed = "bootstrap-staker-1"
	config.ValidatorSeed = "bootstrap-validator-1"
	config.ConsensusSeed = "bootstrap-consensus-1"
	config.BootstrapJoin = bootstrapJoinConfig{
		Enabled:            true,
		RPCURL:             server.URL,
		PollIntervalMillis: 200,
		TimeoutMillis:      1000,
	}
	config.Genesis = genesisConfig{}
	normalized, err := normalizeNodeConfig(config)
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}

	joined, err := prepareBootstrapJoinConfig(context.Background(), normalized, testBootstrapLogger())
	if err != nil {
		t.Fatalf("prepareBootstrapJoinConfig() error = %v", err)
	}
	if joined.ChainID != manifest.ChainID {
		t.Fatalf("ChainID = %q, want %q", joined.ChainID, manifest.ChainID)
	}
	if joined.ChainIdentityHash != manifest.ChainIdentityHash {
		t.Fatalf("ChainIdentityHash = %q, want %q", joined.ChainIdentityHash, manifest.ChainIdentityHash)
	}
	if len(joined.Genesis.InitialValidators) != len(manifest.Genesis.InitialValidators) {
		t.Fatalf("validators = %d, want %d", len(joined.Genesis.InitialValidators), len(manifest.Genesis.InitialValidators))
	}
}

func TestPrepareBootstrapJoinRejectsFrozenManifestForUnknownValidator(t *testing.T) {
	coordinatorConfig := testBootstrapCoordinatorConfig(t, 1)
	coordinator, err := newBootstrapCoordinator(coordinatorConfig, testBootstrapLogger())
	if err != nil {
		t.Fatalf("newBootstrapCoordinator() error = %v", err)
	}
	registration := testBootstrapRegistration(t, coordinatorConfig, 1)
	if _, err := coordinator.BootstrapRegisterValidator(context.Background(), registration); err != nil {
		t.Fatalf("BootstrapRegisterValidator() error = %v", err)
	}
	manifest, err := coordinator.GetBootstrapManifest(context.Background())
	if err != nil {
		t.Fatalf("GetBootstrapManifest() error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		decoded := bootstrapRPCRequest{}
		if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil {
			t.Fatalf("Decode(request) error = %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		if decoded.Method == rpc.MethodGetBootstrapManifest {
			_ = json.NewEncoder(writer).Encode(bootstrapRPCResponse{
				JSONRPC: bootstrapRPCVersion,
				ID:      decoded.ID,
				Result:  mustMarshalRawMessage(t, manifest),
			})
			return
		}
		_ = json.NewEncoder(writer).Encode(bootstrapRPCResponse{
			JSONRPC: bootstrapRPCVersion,
			ID:      decoded.ID,
			Error: &rpc.Error{
				Code:    -32603,
				Message: "internal error",
				Data:    "bootstrap register validator: posnode: bootstrap manifest already frozen",
			},
		})
	}))
	defer server.Close()

	config := minimalNodeConfigForValidation()
	config.NodeName = "validator-unknown"
	config.PeerSeed = "unknown-peer"
	config.BootstrapJoin = bootstrapJoinConfig{
		Enabled:            true,
		RPCURL:             server.URL,
		PollIntervalMillis: 200,
		TimeoutMillis:      1000,
	}
	config.Genesis = genesisConfig{}
	normalized, err := normalizeNodeConfig(config)
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}

	_, err = prepareBootstrapJoinConfig(context.Background(), normalized, testBootstrapLogger())
	if err == nil || !strings.Contains(err.Error(), "does not include local validator") {
		t.Fatalf("prepareBootstrapJoinConfig() error = %v, want unknown validator rejection", err)
	}
}

func mustMarshalRawMessage(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(value) error = %v", err)
	}
	return data
}

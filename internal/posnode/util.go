package posnode

import (
	"encoding/json"
	"fmt"

	"solana_golang/consensus"
	"solana_golang/structure"
	"solana_golang/utils"
)

func jsonUnmarshal(data []byte, value interface{}) error {
	if len(data) == 0 {
		return fmt.Errorf("posnode: empty json payload")
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("posnode: decode json payload: %w", err)
	}
	return nil
}

func hashQC(qc consensus.QuorumCertificate) (structure.Hash, error) {
	data, err := qc.MarshalBinary()
	if err != nil {
		return structure.Hash{}, err
	}
	return structure.NewHash(utils.SHA256(data))
}

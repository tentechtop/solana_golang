package zk

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"sync"
)

const (
	// DefaultShieldedParametersSHA256 固定发布包中的完整参数字节，任何变化都必须走共识分支升级。
	DefaultShieldedParametersSHA256 = "81a37e6d8be384f31f60a7b483f7ce6be33cebf0e922bcfe860aa9f25d291954"
	// DefaultShieldedConstraintSystemSHA256 固定由当前源码编译得到的 R1CS 布局。
	DefaultShieldedConstraintSystemSHA256 = "5525508a116c49fa8cde44361109addb8579449189c0bfe4d3254516fc3e35e7"
	// DefaultShieldedVerifyingKeySHA256 固定创世池接受的验证密钥。
	DefaultShieldedVerifyingKeySHA256 = "2c68f8a51246e48b5dc3fe137b4925311180d57d606c91d7f51587ec334b6c4e"
)

//go:embed parameters/shielded_v2.params
var embeddedShieldedParameters []byte

var (
	defaultShieldedParametersOnce sync.Once
	defaultShieldedParameters     *ShieldedCircuitParameters
	defaultShieldedParametersErr  error
)

// DefaultShieldedCircuitParameters 返回协议内置 V2 开发参数 + 所有节点必须使用相同字节和验证密钥哈希。
// 该参数集来自单方 setup，只允许本地开发和测试；生产网络必须替换为可审计的 MPC 仪式产物。
func DefaultShieldedCircuitParameters() (*ShieldedCircuitParameters, error) {
	defaultShieldedParametersOnce.Do(func() {
		parameterDigest := sha256.Sum256(embeddedShieldedParameters)
		if fmt.Sprintf("%x", parameterDigest) != DefaultShieldedParametersSHA256 {
			defaultShieldedParametersErr = fmt.Errorf("zk: embedded shielded parameters checksum mismatch")
			return
		}
		defaultShieldedParameters, defaultShieldedParametersErr = UnmarshalShieldedCircuitParameters(embeddedShieldedParameters)
		if defaultShieldedParametersErr != nil {
			defaultShieldedParametersErr = fmt.Errorf("zk: load embedded shielded parameters: %w", defaultShieldedParametersErr)
			return
		}
		defaultShieldedParametersErr = validateDefaultShieldedParameterLayout(defaultShieldedParameters)
		if defaultShieldedParametersErr != nil {
			defaultShieldedParameters = nil
		}
	})
	return defaultShieldedParameters, defaultShieldedParametersErr
}

func validateDefaultShieldedParameterLayout(parameters *ShieldedCircuitParameters) error {
	verifyingKeyHash, err := parameters.VerifyingKeyHash()
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", verifyingKeyHash) != DefaultShieldedVerifyingKeySHA256 {
		return fmt.Errorf("zk: embedded shielded verifying key mismatch")
	}

	parameterConstraintSystem, err := writeShieldedParameterSection(parameters.ConstraintSystem)
	if err != nil {
		return fmt.Errorf("zk: encode embedded shielded constraint system: %w", err)
	}
	parameterConstraintSystemHash := sha256.Sum256(parameterConstraintSystem)
	if fmt.Sprintf("%x", parameterConstraintSystemHash) != DefaultShieldedConstraintSystemSHA256 {
		return fmt.Errorf("zk: embedded shielded constraint system mismatch")
	}

	compiledConstraintSystem, err := CompileShieldedCircuit()
	if err != nil {
		return err
	}
	compiledConstraintSystemBytes, err := writeShieldedParameterSection(compiledConstraintSystem)
	if err != nil {
		return fmt.Errorf("zk: encode compiled shielded constraint system: %w", err)
	}
	compiledConstraintSystemHash := sha256.Sum256(compiledConstraintSystemBytes)
	if fmt.Sprintf("%x", compiledConstraintSystemHash) != DefaultShieldedConstraintSystemSHA256 {
		return fmt.Errorf("zk: shielded circuit source does not match embedded parameters")
	}
	return nil
}

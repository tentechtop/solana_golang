package posnode

import (
	"testing"

	"github.com/tentechtop/ordexus-core/database"
)

func TestRewardConfigUsesNetworkActivationEpoch(t *testing.T) {
	for _, epoch := range []uint64{0, 32} {
		config := nodeConfig{StakeRewardExactRatioEpoch: epoch}
		if got := config.rewardConfig().ExactStakePayoutActivationEpoch; got != epoch {
			t.Fatalf("activation epoch = %d, want %d", got, epoch)
		}
	}
}

func TestStakePayoutScheduleCannotChangeOnRestart(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	node.config.StakeRewardExactRatioEpoch = 32
	if err := node.ensureStakePayoutSchedule(); err != nil {
		t.Fatal(err)
	}
	if err := node.ensureStakePayoutSchedule(); err != nil {
		t.Fatal(err)
	}
	node.config.StakeRewardExactRatioEpoch = 0
	if err := node.ensureStakePayoutSchedule(); err == nil {
		t.Fatal("restart accepted a different reward schedule")
	}
}

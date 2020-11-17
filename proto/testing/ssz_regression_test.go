package testing

import (
	"fmt"
	"io/ioutil"
	"testing"

	ethpb "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
)

func TestFastSSZBitlistBug(t *testing.T) {
	// See fix: https://github.com/ferranbt/fastssz/pull/18
	// beacon_block_151906.ssz hashes to 0x684fd51e500001fd596ef4d4061863b6713846133f7d48e828cee4e15a0d7978.
	// This test can be removed when this case is included as an upstream spec test.
	b, err := ioutil.ReadFile("data/beacon_block_151906.ssz")
	if err != nil {
		t.Fatal(err)
	}
	sb := &ethpb.SignedBeaconBlock{}
	if err := sb.UnmarshalSSZ(b); err != nil {
		t.Fatal(err)
	}
	r, err := sb.HashTreeRoot()
	if err != nil {
		t.Fatal(err)
	}
	if rStr := fmt.Sprintf("%#x", r); rStr != "0x684fd51e500001fd596ef4d4061863b6713846133f7d48e828cee4e15a0d7978" {
		t.Errorf("Received wrong root. Got %s, wanted %s", rStr, "0x684fd51e500001fd596ef4d4061863b6713846133f7d48e828cee4e15a0d7978")
	}
}

// Package client represents a gRPC polling-based implementation
// of an eth2 validator client.
package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dgraph-io/ristretto"
	"github.com/gogo/protobuf/proto"
	ptypes "github.com/gogo/protobuf/types"
	lru "github.com/hashicorp/golang-lru"
	"github.com/pkg/errors"
	ethpb "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/helpers"
	"github.com/prysmaticlabs/prysm/shared/bytesutil"
	"github.com/prysmaticlabs/prysm/shared/event"
	"github.com/prysmaticlabs/prysm/shared/featureconfig"
	"github.com/prysmaticlabs/prysm/shared/hashutil"
	"github.com/prysmaticlabs/prysm/shared/params"
	"github.com/prysmaticlabs/prysm/shared/slotutil"
	"github.com/prysmaticlabs/prysm/validator/accounts/wallet"
	vdb "github.com/prysmaticlabs/prysm/validator/db"
	"github.com/prysmaticlabs/prysm/validator/db/kv"
	"github.com/prysmaticlabs/prysm/validator/keymanager"
	slashingprotection "github.com/prysmaticlabs/prysm/validator/slashing-protection"
	"github.com/sirupsen/logrus"
	"go.opencensus.io/trace"
)

// reconnectPeriod is the frequency that we try to restart our
// slasher connection when the slasher client connection is not ready.
var reconnectPeriod = 5 * time.Second

// ValidatorRole defines the validator role.
type ValidatorRole int8

const (
	roleUnknown = iota
	roleAttester
	roleProposer
	roleAggregator
)

type validator struct {
	logValidatorBalances               bool
	useWeb                             bool
	emitAccountMetrics                 bool
	domainDataLock                     sync.Mutex
	attLogsLock                        sync.Mutex
	aggregatedSlotCommitteeIDCacheLock sync.Mutex
	prevBalanceLock                    sync.RWMutex
	attesterHistoryByPubKeyLock        sync.RWMutex
	walletInitializedFeed              *event.Feed
	genesisTime                        uint64
	domainDataCache                    *ristretto.Cache
	aggregatedSlotCommitteeIDCache     *lru.Cache
	ticker                             *slotutil.SlotTicker
	attesterHistoryByPubKey            map[[48]byte]kv.EncHistoryData
	prevBalance                        map[[48]byte]uint64
	duties                             *ethpb.DutiesResponse
	startBalances                      map[[48]byte]uint64
	attLogs                            map[[32]byte]*attSubmitted
	node                               ethpb.NodeClient
	keyManager                         keymanager.IKeymanager
	beaconClient                       ethpb.BeaconChainClient
	validatorClient                    ethpb.BeaconNodeValidatorClient
	protector                          slashingprotection.Protector
	db                                 vdb.Database
	graffiti                           []byte
	voteStats                          voteStats
}

// Done cleans up the validator.
func (v *validator) Done() {
	v.ticker.Done()
}

// WaitForWalletInitialization checks if the validator needs to wait for
func (v *validator) WaitForWalletInitialization(ctx context.Context) error {
	// This function should only run if we are using managing the
	// validator client using the Prysm web UI.
	if !v.useWeb {
		return nil
	}
	if v.keyManager != nil {
		return nil
	}
	walletChan := make(chan *wallet.Wallet)
	sub := v.walletInitializedFeed.Subscribe(walletChan)
	defer sub.Unsubscribe()
	for {
		select {
		case w := <-walletChan:
			keyManager, err := w.InitializeKeymanager(ctx)
			if err != nil {
				return errors.Wrap(err, "could not read keymanager")
			}
			v.keyManager = keyManager
			return nil
		case <-ctx.Done():
			return errors.New("context canceled")
		}
	}
}

// WaitForChainStart checks whether the beacon node has started its runtime. That is,
// it calls to the beacon node which then verifies the ETH1.0 deposit contract logs to check
// for the ChainStart log to have been emitted. If so, it starts a ticker based on the ChainStart
// unix timestamp which will be used to keep track of time within the validator client.
func (v *validator) WaitForChainStart(ctx context.Context) error {
	ctx, span := trace.StartSpan(ctx, "validator.WaitForChainStart")
	defer span.End()
	// First, check if the beacon chain has started.
	stream, err := v.validatorClient.WaitForChainStart(ctx, &ptypes.Empty{})
	if err != nil {
		return errors.Wrap(err, "could not setup beacon chain ChainStart streaming client")
	}

	log.Info("Waiting for beacon chain start log from the ETH 1.0 deposit contract")
	chainStartRes, err := stream.Recv()
	if err != io.EOF {
		if ctx.Err() == context.Canceled {
			return errors.Wrap(ctx.Err(), "context has been canceled so shutting down the loop")
		}
		if err != nil {
			return errors.Wrap(err, "could not receive ChainStart from stream")
		}
		v.genesisTime = chainStartRes.GenesisTime
		curGenValRoot, err := v.db.GenesisValidatorsRoot(ctx)
		if err != nil {
			return errors.Wrap(err, "could not get current genesis validators root")
		}
		if len(curGenValRoot) == 0 {
			if err := v.db.SaveGenesisValidatorsRoot(ctx, chainStartRes.GenesisValidatorsRoot); err != nil {
				return errors.Wrap(err, "could not save genesis validator root")
			}
		} else {
			if !bytes.Equal(curGenValRoot, chainStartRes.GenesisValidatorsRoot) {
				log.Errorf("The genesis validators root received from the beacon node does not match what is in " +
					"your validator database. This could indicate that this is a database meant for another network. If " +
					"you were previously running this validator database on another network, please run --clear-db to " +
					"clear the database. If not, please file an issue at https://github.com/prysmaticlabs/prysm/issues")
				return fmt.Errorf(
					"genesis validators root from beacon node (%#x) does not match root saved in validator db (%#x)",
					chainStartRes.GenesisValidatorsRoot,
					curGenValRoot,
				)
			}
		}
	}

	// Once the ChainStart log is received, we update the genesis time of the validator client
	// and begin a slot ticker used to track the current slot the beacon node is in.
	v.ticker = slotutil.GetSlotTicker(time.Unix(int64(v.genesisTime), 0), params.BeaconConfig().SecondsPerSlot)
	log.WithField("genesisTime", time.Unix(int64(v.genesisTime), 0)).Info("Beacon chain started")
	return nil
}

// WaitForSync checks whether the beacon node has sync to the latest head.
func (v *validator) WaitForSync(ctx context.Context) error {
	ctx, span := trace.StartSpan(ctx, "validator.WaitForSync")
	defer span.End()

	s, err := v.node.GetSyncStatus(ctx, &ptypes.Empty{})
	if err != nil {
		return errors.Wrap(err, "could not get sync status")
	}
	if !s.Syncing {
		return nil
	}

	for {
		select {
		// Poll every half slot.
		case <-time.After(slotutil.DivideSlotBy(2 /* twice per slot */)):
			s, err := v.node.GetSyncStatus(ctx, &ptypes.Empty{})
			if err != nil {
				return errors.Wrap(err, "could not get sync status")
			}
			if !s.Syncing {
				return nil
			}
			log.Info("Waiting for beacon node to sync to latest chain head")
		case <-ctx.Done():
			return errors.New("context has been canceled, exiting goroutine")
		}
	}
}

// SlasherReady checks if slasher that was configured as external protection
// is reachable.
func (v *validator) SlasherReady(ctx context.Context) error {
	ctx, span := trace.StartSpan(ctx, "validator.SlasherReady")
	defer span.End()
	if featureconfig.Get().SlasherProtection {
		err := v.protector.Status()
		if err == nil {
			return nil
		}
		ticker := time.NewTicker(reconnectPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				log.WithError(err).Info("Slasher connection wasn't ready. Trying again")
				err = v.protector.Status()
				if err != nil {
					continue
				}
				log.Info("Slasher connection is ready")
				return nil
			case <-ctx.Done():
				log.Debug("Context closed, exiting reconnect external protection")
				return errors.New("context closed, no longer attempting to restart external protection")
			}
		}
	}
	return nil
}

// WaitForActivation checks whether the validator pubkey is in the active
// validator set. If not, this operation will block until an activation message is
// received.
func (v *validator) WaitForActivation(ctx context.Context) error {
	ctx, span := trace.StartSpan(ctx, "validator.WaitForActivation")
	defer span.End()

	validatingKeys, err := v.keyManager.FetchValidatingPublicKeys(ctx)
	if err != nil {
		return errors.Wrap(err, "could not fetch validating keys")
	}
	req := &ethpb.ValidatorActivationRequest{
		PublicKeys: bytesutil.FromBytes48Array(validatingKeys),
	}
	stream, err := v.validatorClient.WaitForActivation(ctx, req)
	if err != nil {
		return errors.Wrap(err, "could not setup validator WaitForActivation streaming client")
	}
	for {
		res, err := stream.Recv()
		// If the stream is closed, we stop the loop.
		if errors.Is(err, io.EOF) {
			break
		}
		// If context is canceled we stop the loop.
		if ctx.Err() == context.Canceled {
			return errors.Wrap(ctx.Err(), "context has been canceled so shutting down the loop")
		}
		if err != nil {
			return errors.Wrap(err, "could not receive validator activation from stream")
		}
		valActivated := v.checkAndLogValidatorStatus(res.Statuses)

		if valActivated {
			for _, statusResp := range res.Statuses {
				if statusResp.Status.Status != ethpb.ValidatorStatus_ACTIVE {
					continue
				}
				log.WithFields(logrus.Fields{
					"publicKey": fmt.Sprintf("%#x", bytesutil.Trunc(statusResp.PublicKey)),
					"index":     statusResp.Index,
				}).Info("Validator activated")
			}
			break
		}
	}
	v.ticker = slotutil.GetSlotTicker(time.Unix(int64(v.genesisTime), 0), params.BeaconConfig().SecondsPerSlot)

	return nil
}

func (v *validator) checkAndLogValidatorStatus(validatorStatuses []*ethpb.ValidatorActivationResponse_Status) bool {
	nonexistentIndex := ^uint64(0)
	var validatorActivated bool
	for _, status := range validatorStatuses {
		fields := logrus.Fields{
			"pubKey": fmt.Sprintf("%#x", bytesutil.Trunc(status.PublicKey)),
			"status": status.Status.Status.String(),
		}
		if status.Index != nonexistentIndex {
			fields["index"] = status.Index
		}
		log := log.WithFields(fields)
		if v.emitAccountMetrics {
			fmtKey := fmt.Sprintf("%#x", status.PublicKey)
			ValidatorStatusesGaugeVec.WithLabelValues(fmtKey).Set(float64(status.Status.Status))
		}
		switch status.Status.Status {
		case ethpb.ValidatorStatus_UNKNOWN_STATUS:
			log.Info("Waiting for deposit to be observed by beacon node")
		case ethpb.ValidatorStatus_DEPOSITED:
			if status.Status.DepositInclusionSlot != 0 {
				log.WithFields(logrus.Fields{
					"expectedInclusionSlot":  status.Status.DepositInclusionSlot,
					"eth1DepositBlockNumber": status.Status.Eth1DepositBlockNumber,
				}).Info("Deposit for validator received but not processed into the beacon state")
			} else {
				log.WithField(
					"positionInActivationQueue", status.Status.PositionInActivationQueue,
				).Info("Deposit processed, entering activation queue after finalization")
			}
		case ethpb.ValidatorStatus_PENDING:
			if status.Status.ActivationEpoch == params.BeaconConfig().FarFutureEpoch {
				log.WithFields(logrus.Fields{
					"positionInActivationQueue": status.Status.PositionInActivationQueue,
				}).Info("Waiting to be assigned activation epoch")
			} else {
				log.WithFields(logrus.Fields{
					"activationEpoch": status.Status.ActivationEpoch,
				}).Info("Waiting for activation")
			}
		case ethpb.ValidatorStatus_ACTIVE, ethpb.ValidatorStatus_EXITING:
			validatorActivated = true
		case ethpb.ValidatorStatus_EXITED:
			log.Info("Validator exited")
		case ethpb.ValidatorStatus_INVALID:
			log.Warn("Invalid Eth1 deposit")
		default:
			log.WithFields(logrus.Fields{
				"activationEpoch": status.Status.ActivationEpoch,
			}).Info("Validator status")
		}
	}
	return validatorActivated
}

// CanonicalHeadSlot returns the slot of canonical block currently found in the
// beacon chain via RPC.
func (v *validator) CanonicalHeadSlot(ctx context.Context) (uint64, error) {
	ctx, span := trace.StartSpan(ctx, "validator.CanonicalHeadSlot")
	defer span.End()
	head, err := v.beaconClient.GetChainHead(ctx, &ptypes.Empty{})
	if err != nil {
		return 0, err
	}
	return head.HeadSlot, nil
}

// NextSlot emits the next slot number at the start time of that slot.
func (v *validator) NextSlot() <-chan uint64 {
	return v.ticker.C()
}

// SlotDeadline is the start time of the next slot.
func (v *validator) SlotDeadline(slot uint64) time.Time {
	secs := (slot + 1) * params.BeaconConfig().SecondsPerSlot
	return time.Unix(int64(v.genesisTime), 0 /*ns*/).Add(time.Duration(secs) * time.Second)
}

// UpdateDuties checks the slot number to determine if the validator's
// list of upcoming assignments needs to be updated. For example, at the
// beginning of a new epoch.
func (v *validator) UpdateDuties(ctx context.Context, slot uint64) error {
	if slot%params.BeaconConfig().SlotsPerEpoch != 0 && v.duties != nil {
		// Do nothing if not epoch start AND assignments already exist.
		return nil
	}
	// Set deadline to end of epoch.
	ss, err := helpers.StartSlot(helpers.SlotToEpoch(slot) + 1)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithDeadline(ctx, v.SlotDeadline(ss))
	defer cancel()
	ctx, span := trace.StartSpan(ctx, "validator.UpdateAssignments")
	defer span.End()

	validatingKeys, err := v.keyManager.FetchValidatingPublicKeys(ctx)
	if err != nil {
		return err
	}
	req := &ethpb.DutiesRequest{
		Epoch:      slot / params.BeaconConfig().SlotsPerEpoch,
		PublicKeys: bytesutil.FromBytes48Array(validatingKeys),
	}

	// If duties is nil it means we have had no prior duties and just started up.
	resp, err := v.validatorClient.GetDuties(ctx, req)
	if err != nil {
		v.duties = nil // Clear assignments so we know to retry the request.
		log.Error(err)
		return err
	}

	v.duties = resp
	v.logDuties(slot, v.duties.Duties)
	subscribeSlots := make([]uint64, 0, len(validatingKeys))
	subscribeCommitteeIDs := make([]uint64, 0, len(validatingKeys))
	subscribeIsAggregator := make([]bool, 0, len(validatingKeys))
	alreadySubscribed := make(map[[64]byte]bool)

	for _, duty := range v.duties.Duties {
		pk := bytesutil.ToBytes48(duty.PublicKey)
		if duty.Status == ethpb.ValidatorStatus_ACTIVE || duty.Status == ethpb.ValidatorStatus_EXITING {
			attesterSlot := duty.AttesterSlot
			committeeIndex := duty.CommitteeIndex

			alreadySubscribedKey := validatorSubscribeKey(attesterSlot, committeeIndex)
			if _, ok := alreadySubscribed[alreadySubscribedKey]; ok {
				continue
			}

			aggregator, err := v.isAggregator(ctx, duty.Committee, attesterSlot, pk)
			if err != nil {
				return errors.Wrap(err, "could not check if a validator is an aggregator")
			}
			if aggregator {
				alreadySubscribed[alreadySubscribedKey] = true
			}

			subscribeSlots = append(subscribeSlots, attesterSlot)
			subscribeCommitteeIDs = append(subscribeCommitteeIDs, committeeIndex)
			subscribeIsAggregator = append(subscribeIsAggregator, aggregator)
		}
	}

	// Notify beacon node to subscribe to the attester and aggregator subnets for the next epoch.
	req.Epoch++
	dutiesNextEpoch, err := v.validatorClient.GetDuties(ctx, req)
	if err != nil {
		log.Error(err)
		return err
	}
	for _, duty := range dutiesNextEpoch.Duties {
		if duty.Status == ethpb.ValidatorStatus_ACTIVE || duty.Status == ethpb.ValidatorStatus_EXITING {
			attesterSlot := duty.AttesterSlot
			committeeIndex := duty.CommitteeIndex

			alreadySubscribedKey := validatorSubscribeKey(attesterSlot, committeeIndex)
			if _, ok := alreadySubscribed[alreadySubscribedKey]; ok {
				continue
			}

			aggregator, err := v.isAggregator(ctx, duty.Committee, attesterSlot, bytesutil.ToBytes48(duty.PublicKey))
			if err != nil {
				return errors.Wrap(err, "could not check if a validator is an aggregator")
			}
			if aggregator {
				alreadySubscribed[alreadySubscribedKey] = true
			}

			subscribeSlots = append(subscribeSlots, attesterSlot)
			subscribeCommitteeIDs = append(subscribeCommitteeIDs, committeeIndex)
			subscribeIsAggregator = append(subscribeIsAggregator, aggregator)
		}
	}

	_, err = v.validatorClient.SubscribeCommitteeSubnets(ctx, &ethpb.CommitteeSubnetsSubscribeRequest{
		Slots:        subscribeSlots,
		CommitteeIds: subscribeCommitteeIDs,
		IsAggregator: subscribeIsAggregator,
	})

	return err
}

// RolesAt slot returns the validator roles at the given slot. Returns nil if the
// validator is known to not have a roles at the slot. Returns UNKNOWN if the
// validator assignments are unknown. Otherwise returns a valid ValidatorRole map.
func (v *validator) RolesAt(ctx context.Context, slot uint64) (map[[48]byte][]ValidatorRole, error) {
	rolesAt := make(map[[48]byte][]ValidatorRole)
	for _, duty := range v.duties.Duties {
		var roles []ValidatorRole

		if duty == nil {
			continue
		}
		if len(duty.ProposerSlots) > 0 {
			for _, proposerSlot := range duty.ProposerSlots {
				if proposerSlot != 0 && proposerSlot == slot {
					roles = append(roles, roleProposer)
					break
				}
			}
		}
		if duty.AttesterSlot == slot {
			roles = append(roles, roleAttester)

			aggregator, err := v.isAggregator(ctx, duty.Committee, slot, bytesutil.ToBytes48(duty.PublicKey))
			if err != nil {
				return nil, errors.Wrap(err, "could not check if a validator is an aggregator")
			}
			if aggregator {
				roles = append(roles, roleAggregator)
			}

		}
		if len(roles) == 0 {
			roles = append(roles, roleUnknown)
		}

		var pubKey [48]byte
		copy(pubKey[:], duty.PublicKey)
		rolesAt[pubKey] = roles
	}
	return rolesAt, nil
}

// UpdateProtections goes through the duties of the given slot and fetches the required validator history,
// assigning it in validator.
func (v *validator) UpdateProtections(ctx context.Context, slot uint64) error {
	attestingPubKeys := make([][48]byte, 0, len(v.duties.CurrentEpochDuties))
	for _, duty := range v.duties.CurrentEpochDuties {
		if duty == nil || duty.AttesterSlot != slot {
			continue
		}
		attestingPubKeys = append(attestingPubKeys, bytesutil.ToBytes48(duty.PublicKey))
	}
	attHistoryByPubKey, err := v.db.AttestationHistoryForPubKeysV2(ctx, attestingPubKeys)
	if err != nil {
		return errors.Wrap(err, "could not get attester history")
	}
	v.attesterHistoryByPubKeyLock.Lock()
	v.attesterHistoryByPubKey = attHistoryByPubKey
	v.attesterHistoryByPubKeyLock.Unlock()
	return nil
}

// ResetAttesterProtectionData reset validators protection data.
func (v *validator) ResetAttesterProtectionData() {
	v.attesterHistoryByPubKeyLock.Lock()
	v.attesterHistoryByPubKey = make(map[[48]byte]kv.EncHistoryData)
	v.attesterHistoryByPubKeyLock.Unlock()
}

// SaveProtection saves the attestation information currently in validator state.
func (v *validator) SaveProtection(ctx context.Context, pubKey [48]byte) error {
	v.attesterHistoryByPubKeyLock.RLock()
	defer v.attesterHistoryByPubKeyLock.RUnlock()
	if err := v.db.SaveAttestationHistoryForPubKeyV2(ctx, pubKey, v.attesterHistoryByPubKey[pubKey]); err != nil {
		return errors.Wrapf(err, "could not save attester with public key %#x history to DB", pubKey)
	}

	return nil
}

// isAggregator checks if a validator is an aggregator of a given slot, it uses the selection algorithm outlined in:
// https://github.com/ethereum/eth2.0-specs/blob/v0.9.3/specs/validator/0_beacon-chain-validator.md#aggregation-selection
func (v *validator) isAggregator(ctx context.Context, committee []uint64, slot uint64, pubKey [48]byte) (bool, error) {
	modulo := uint64(1)
	if len(committee)/int(params.BeaconConfig().TargetAggregatorsPerCommittee) > 1 {
		modulo = uint64(len(committee)) / params.BeaconConfig().TargetAggregatorsPerCommittee
	}

	slotSig, err := v.signSlot(ctx, pubKey, slot)
	if err != nil {
		return false, err
	}

	b := hashutil.Hash(slotSig)

	return binary.LittleEndian.Uint64(b[:8])%modulo == 0, nil
}

// UpdateDomainDataCaches by making calls for all of the possible domain data. These can change when
// the fork version changes which can happen once per epoch. Although changing for the fork version
// is very rare, a validator should check these data every epoch to be sure the validator is
// participating on the correct fork version.
func (v *validator) UpdateDomainDataCaches(ctx context.Context, slot uint64) {
	for _, d := range [][]byte{
		params.BeaconConfig().DomainRandao[:],
		params.BeaconConfig().DomainBeaconAttester[:],
		params.BeaconConfig().DomainBeaconProposer[:],
		params.BeaconConfig().DomainSelectionProof[:],
		params.BeaconConfig().DomainAggregateAndProof[:],
	} {
		_, err := v.domainData(ctx, helpers.SlotToEpoch(slot), d)
		if err != nil {
			log.WithError(err).Errorf("Failed to update domain data for domain %v", d)
		}
	}
}

// AllValidatorsAreExited informs whether all validators have already exited.
func (v *validator) AllValidatorsAreExited(ctx context.Context) (bool, error) {
	validatingKeys, err := v.keyManager.FetchValidatingPublicKeys(ctx)
	if err != nil {
		return false, errors.Wrap(err, "could not fetch validating keys")
	}
	if len(validatingKeys) == 0 {
		return false, nil
	}
	var publicKeys [][]byte
	for _, key := range validatingKeys {
		copyKey := key
		publicKeys = append(publicKeys, copyKey[:])
	}
	request := &ethpb.MultipleValidatorStatusRequest{
		PublicKeys: publicKeys,
	}
	response, err := v.validatorClient.MultipleValidatorStatus(ctx, request)
	if err != nil {
		return false, err
	}
	if len(response.Statuses) != len(request.PublicKeys) {
		return false, errors.New("number of status responses did not match number of requested keys")
	}
	for _, status := range response.Statuses {
		if status.Status != ethpb.ValidatorStatus_EXITED {
			return false, nil
		}
	}
	return true, nil
}

func (v *validator) domainData(ctx context.Context, epoch uint64, domain []byte) (*ethpb.DomainResponse, error) {
	v.domainDataLock.Lock()
	defer v.domainDataLock.Unlock()

	req := &ethpb.DomainRequest{
		Epoch:  epoch,
		Domain: domain,
	}

	key := strings.Join([]string{strconv.FormatUint(req.Epoch, 10), hex.EncodeToString(req.Domain)}, ",")

	if val, ok := v.domainDataCache.Get(key); ok {
		return proto.Clone(val.(proto.Message)).(*ethpb.DomainResponse), nil
	}

	res, err := v.validatorClient.DomainData(ctx, req)
	if err != nil {
		return nil, err
	}

	v.domainDataCache.Set(key, proto.Clone(res), 1)

	return res, nil
}

func (v *validator) logDuties(slot uint64, duties []*ethpb.DutiesResponse_Duty) {
	attesterKeys := make([][]string, params.BeaconConfig().SlotsPerEpoch)
	for i := range attesterKeys {
		attesterKeys[i] = make([]string, 0)
	}
	proposerKeys := make([]string, params.BeaconConfig().SlotsPerEpoch)
	slotOffset := slot - (slot % params.BeaconConfig().SlotsPerEpoch)

	for _, duty := range duties {
		if v.emitAccountMetrics {
			fmtKey := fmt.Sprintf("%#x", duty.PublicKey)
			ValidatorStatusesGaugeVec.WithLabelValues(fmtKey).Set(float64(duty.Status))
		}

		// Only interested in validators who are attesting/proposing.
		// Note that SLASHING validators will have duties but their results are ignored by the network so we don't bother with them.
		if duty.Status != ethpb.ValidatorStatus_ACTIVE && duty.Status != ethpb.ValidatorStatus_EXITING {
			continue
		}

		validatorKey := fmt.Sprintf("%#x", bytesutil.Trunc(duty.PublicKey))
		attesterIndex := duty.AttesterSlot - slotOffset
		if attesterIndex >= params.BeaconConfig().SlotsPerEpoch {
			log.WithField("duty", duty).Warn("Invalid attester slot")
		} else {
			attesterKeys[duty.AttesterSlot-slotOffset] = append(attesterKeys[duty.AttesterSlot-slotOffset], validatorKey)
		}

		for _, proposerSlot := range duty.ProposerSlots {
			proposerIndex := proposerSlot - slotOffset
			if proposerIndex >= params.BeaconConfig().SlotsPerEpoch {
				log.WithField("duty", duty).Warn("Invalid proposer slot")
			} else {
				proposerKeys[proposerIndex] = validatorKey
			}
		}
	}

	for i := uint64(0); i < params.BeaconConfig().SlotsPerEpoch; i++ {
		if len(attesterKeys[i]) > 0 {
			log.WithField("slot", slotOffset+i).WithField("attesters", len(attesterKeys[i])).WithField("pubKeys", attesterKeys[i]).Info("Attestation schedule")
		}
		if proposerKeys[i] != "" {
			log.WithField("slot", slotOffset+i).WithField("pubKey", proposerKeys[i]).Info("Proposal schedule")
		}
	}
}

// This constructs a validator subscribed key, it's used to track
// which subnet has already been pending requested.
func validatorSubscribeKey(slot, committeeID uint64) [64]byte {
	return bytesutil.ToBytes64(append(bytesutil.Bytes32(slot), bytesutil.Bytes32(committeeID)...))
}

// This tracks all validators' voting status.
type voteStats struct {
	startEpoch            uint64
	includedAttestedCount uint64
	totalAttestedCount    uint64
	totalDistance         uint64
	correctSources        uint64
	totalSources          uint64
	correctTargets        uint64
	totalTargets          uint64
	correctHeads          uint64
	totalHeads            uint64
}

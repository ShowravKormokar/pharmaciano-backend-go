// Package enums contains typed string aliases for every closed-value column in the database.
package enums

import (
	"encoding/json"
	"fmt"
	"strings"
)

// contains is a tiny helper used by every Valid() below.
func contains[T ~string](vs []T, v T) bool {
	for _, x := range vs {
		if x == v {
			return true
		}
	}
	return false
}

// normalizeLower trims and lower-cases a raw input; used by ParseEnum.
func normalizeLower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// ParseEnum validates raw (trimmed, case-insensitively) against all and
// returns the matching value.
func ParseEnum[T ~string](all []T, raw string) (T, error) {
	var zero T
	norm := T(normalizeLower(raw))
	if contains(all, norm) {
		return norm, nil
	}
	return zero, fmt.Errorf("enums: invalid value %q, want one of %v", raw, all)
}

// unmarshalEnumJSON is the shared body behind every enum's UnmarshalJSON.
func unmarshalEnumJSON[T ~string](data []byte, all []T, dst *T) error {
	if string(data) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("enums: %w", err)
	}
	v, err := ParseEnum(all, s)
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

//  User status  (users.status)

type UserStatus string

const (
	UserStatusActive      UserStatus = "active"
	UserStatusDeactivated UserStatus = "deactivated"
	UserStatusInactive    UserStatus = "inactive"
	UserStatusSuspended   UserStatus = "suspended"
	UserStatusResigned    UserStatus = "resigned"
	UserStatusTerminated  UserStatus = "terminated"
)

var AllUserStatuses = []UserStatus{
	UserStatusActive, UserStatusDeactivated, UserStatusInactive,
	UserStatusSuspended, UserStatusResigned, UserStatusTerminated,
}

func (s UserStatus) String() string { return string(s) }
func (s UserStatus) Valid() bool    { return contains(AllUserStatuses, s) }

// CanLogin reports whether a user in this status is allowed to authenticate.
func (s UserStatus) CanLogin() bool { return s == UserStatusActive }

func ParseUserStatus(s string) (UserStatus, error) { return ParseEnum(AllUserStatuses, s) }
func (s *UserStatus) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllUserStatuses, s)
}

//  User stage  (users.stage)

type UserStage string

const (
	UserStageUnverified UserStage = "unverified"
	UserStagePending    UserStage = "pending"
	UserStageVerified   UserStage = "verified"
)

var AllUserStages = []UserStage{UserStageUnverified, UserStagePending, UserStageVerified}

func (s UserStage) String() string { return string(s) }
func (s UserStage) Valid() bool    { return contains(AllUserStages, s) }

func ParseUserStage(s string) (UserStage, error) { return ParseEnum(AllUserStages, s) }
func (s *UserStage) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllUserStages, s)
}

//  Employment type

type EmploymentType string

const (
	EmploymentFullTime EmploymentType = "full_time"
	EmploymentPartTime EmploymentType = "part_time"
	EmploymentContract EmploymentType = "contract"
	EmploymentIntern   EmploymentType = "intern"
)

var AllEmploymentTypes = []EmploymentType{
	EmploymentFullTime, EmploymentPartTime, EmploymentContract, EmploymentIntern,
}

func (e EmploymentType) String() string { return string(e) }
func (e EmploymentType) Valid() bool    { return contains(AllEmploymentTypes, e) }

func ParseEmploymentType(s string) (EmploymentType, error) {
	return ParseEnum(AllEmploymentTypes, s)
}

func (e *EmploymentType) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllEmploymentTypes, e)
}

//  Subscription plan

type SubscriptionPlan string

const (
	SubscriptionFree       SubscriptionPlan = "free"
	SubscriptionPro        SubscriptionPlan = "pro"
	SubscriptionEnterprise SubscriptionPlan = "enterprise"
)

var AllSubscriptionPlans = []SubscriptionPlan{
	SubscriptionFree, SubscriptionPro, SubscriptionEnterprise,
}

func (p SubscriptionPlan) String() string { return string(p) }
func (p SubscriptionPlan) Valid() bool {
	return contains(AllSubscriptionPlans, p)
}

func ParseSubscriptionPlan(s string) (SubscriptionPlan, error) {
	return ParseEnum(AllSubscriptionPlans, s)
}
func (p *SubscriptionPlan) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllSubscriptionPlans, p)
}

//  Address kind

type AddressKind string

const (
	AddressPresent   AddressKind = "present"
	AddressPermanent AddressKind = "permanent"
	AddressMailing   AddressKind = "mailing"
)

var AllAddressKinds = []AddressKind{AddressPresent, AddressPermanent, AddressMailing}

func (k AddressKind) String() string { return string(k) }
func (k AddressKind) Valid() bool    { return contains(AllAddressKinds, k) }

func ParseAddressKind(s string) (AddressKind, error) { return ParseEnum(AllAddressKinds, s) }
func (k *AddressKind) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllAddressKinds, k)
}

//  Gender

type Gender string

const (
	GenderMale   Gender = "male"
	GenderFemale Gender = "female"
	GenderOther  Gender = "other"
)

var AllGenders = []Gender{GenderMale, GenderFemale, GenderOther}

func (g Gender) String() string { return string(g) }
func (g Gender) Valid() bool    { return contains(AllGenders, g) }

func ParseGender(s string) (Gender, error) { return ParseEnum(AllGenders, s) }
func (g *Gender) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllGenders, g)
}

//  Purchase state machine

type PurchaseState string

const (
	PurchaseStateDraft             PurchaseState = "draft"
	PurchaseStatePendingApproval   PurchaseState = "pending_approval"
	PurchaseStateApproved          PurchaseState = "approved"
	PurchaseStateRejected          PurchaseState = "rejected"
	PurchaseStateDispatched        PurchaseState = "dispatched"
	PurchaseStatePartiallyReceived PurchaseState = "partially_received"
	PurchaseStateReceived          PurchaseState = "received"
	PurchaseStateCompleted         PurchaseState = "completed"
	PurchaseStateCancelled         PurchaseState = "cancelled"
)

var AllPurchaseStates = []PurchaseState{
	PurchaseStateDraft, PurchaseStatePendingApproval, PurchaseStateApproved,
	PurchaseStateRejected, PurchaseStateDispatched, PurchaseStatePartiallyReceived,
	PurchaseStateReceived, PurchaseStateCompleted, PurchaseStateCancelled,
}

func (s PurchaseState) String() string { return string(s) }
func (s PurchaseState) Valid() bool    { return contains(AllPurchaseStates, s) }

func ParsePurchaseState(s string) (PurchaseState, error) { return ParseEnum(AllPurchaseStates, s) }
func (s *PurchaseState) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllPurchaseStates, s)
}

// purchaseTransitions is the canonical purchase workflow graph, computed
// once at package load instead of allocating a fresh map on every
// CanTransitionTo call — that call sits on a hot path (every purchase
// approve/receive/cancel action, including bulk imports).
var purchaseTransitions = map[PurchaseState][]PurchaseState{
	PurchaseStateDraft:             {PurchaseStatePendingApproval, PurchaseStateCancelled},
	PurchaseStatePendingApproval:   {PurchaseStateApproved, PurchaseStateRejected, PurchaseStateCancelled},
	PurchaseStateApproved:          {PurchaseStateDispatched, PurchaseStateCancelled},
	PurchaseStateDispatched:        {PurchaseStatePartiallyReceived, PurchaseStateReceived, PurchaseStateCancelled},
	PurchaseStatePartiallyReceived: {PurchaseStateReceived, PurchaseStateCancelled},
	PurchaseStateReceived:          {PurchaseStateCompleted},
}

// CanTransitionTo reports whether a state may move to `next`. This is the
// canonical purchase workflow — the service layer must call this before
// every state change.
func (s PurchaseState) CanTransitionTo(next PurchaseState) bool {
	nexts, ok := purchaseTransitions[s]
	if !ok {
		return false
	}
	return contains(nexts, next)
}

// IsTerminal reports whether the purchase is in a final state.
func (s PurchaseState) IsTerminal() bool {
	return s == PurchaseStateCompleted || s == PurchaseStateCancelled || s == PurchaseStateRejected
}

//  Payment status  (purchases.payment_status / sales.payment_status)

type PaymentStatus string

const (
	PaymentStatusUnpaid   PaymentStatus = "unpaid"
	PaymentStatusPartial  PaymentStatus = "partial"
	PaymentStatusPaid     PaymentStatus = "paid"
	PaymentStatusVoid     PaymentStatus = "void"
	PaymentStatusRefunded PaymentStatus = "refunded"
)

var AllPaymentStatuses = []PaymentStatus{
	PaymentStatusUnpaid, PaymentStatusPartial, PaymentStatusPaid,
	PaymentStatusVoid, PaymentStatusRefunded,
}

func (p PaymentStatus) String() string { return string(p) }
func (p PaymentStatus) Valid() bool    { return contains(AllPaymentStatuses, p) }

func ParsePaymentStatus(s string) (PaymentStatus, error) { return ParseEnum(AllPaymentStatuses, s) }
func (p *PaymentStatus) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllPaymentStatuses, p)
}

//  Payment method / type

type PaymentType string

const (
	PaymentTypeCash       PaymentType = "cash"
	PaymentTypeCard       PaymentType = "card"
	PaymentTypeMobile     PaymentType = "mobile" // bKash / Nagad / Rocket
	PaymentTypeBank       PaymentType = "bank"   // bank transfer / cheque deposit
	PaymentTypeCheque     PaymentType = "cheque"
	PaymentTypeMixed      PaymentType = "mixed"      // multi-tender POS sale
	PaymentTypeAdjustment PaymentType = "adjustment" // credit note, refund voucher
	PaymentTypeCredit     PaymentType = "credit"     // pay-later / customer receivable
)

var AllPaymentTypes = []PaymentType{
	PaymentTypeCash, PaymentTypeCard, PaymentTypeMobile, PaymentTypeBank,
	PaymentTypeCheque, PaymentTypeMixed, PaymentTypeAdjustment, PaymentTypeCredit,
}

func (t PaymentType) String() string { return string(t) }
func (t PaymentType) Valid() bool    { return contains(AllPaymentTypes, t) }

func ParsePaymentType(s string) (PaymentType, error) { return ParseEnum(AllPaymentTypes, s) }
func (t *PaymentType) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllPaymentTypes, t)
}

//  Sale state

type SaleState string

const (
	SaleStateCompleted         SaleState = "completed"
	SaleStatePartiallyRefunded SaleState = "partially_refunded"
	SaleStateRefunded          SaleState = "refunded"
	SaleStateCancelled         SaleState = "cancelled"
	SaleStateVoided            SaleState = "voided"
)

var AllSaleStates = []SaleState{
	SaleStateCompleted, SaleStatePartiallyRefunded, SaleStateRefunded,
	SaleStateCancelled, SaleStateVoided,
}

func (s SaleState) String() string { return string(s) }
func (s SaleState) Valid() bool    { return contains(AllSaleStates, s) }

func ParseSaleState(s string) (SaleState, error) { return ParseEnum(AllSaleStates, s) }
func (s *SaleState) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllSaleStates, s)
}

//  Sale type

type SaleType string

const (
	SaleTypeRetail    SaleType = "retail"
	SaleTypeWholesale SaleType = "wholesale"
	SaleTypeOnline    SaleType = "online"
)

var AllSaleTypes = []SaleType{SaleTypeRetail, SaleTypeWholesale, SaleTypeOnline}

func (t SaleType) String() string { return string(t) }
func (t SaleType) Valid() bool    { return contains(AllSaleTypes, t) }

func ParseSaleType(s string) (SaleType, error) { return ParseEnum(AllSaleTypes, s) }
func (t *SaleType) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllSaleTypes, t)
}

//  Pack type (POS line item)

type PackType string

const (
	PackTypeUnit  PackType = "unit"
	PackTypeStrip PackType = "strip"
	PackTypePack  PackType = "pack"
	PackTypeBox   PackType = "box"
)

var AllPackTypes = []PackType{PackTypeUnit, PackTypeStrip, PackTypePack, PackTypeBox}

func (p PackType) String() string { return string(p) }
func (p PackType) Valid() bool    { return contains(AllPackTypes, p) }

func ParsePackType(s string) (PackType, error) { return ParseEnum(AllPackTypes, s) }
func (p *PackType) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllPackTypes, p)
}

//  Discount / charge kind

type ChargeKind string

const (
	ChargeKindPercent ChargeKind = "percent"
	ChargeKindFlat    ChargeKind = "flat"
)

var AllChargeKinds = []ChargeKind{ChargeKindPercent, ChargeKindFlat}

func (c ChargeKind) String() string { return string(c) }
func (c ChargeKind) Valid() bool    { return contains(AllChargeKinds, c) }

func ParseChargeKind(s string) (ChargeKind, error) { return ParseEnum(AllChargeKinds, s) }
func (c *ChargeKind) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllChargeKinds, c)
}

//  Inventory batch status

type BatchStatus string

const (
	BatchStatusActive    BatchStatus = "active"
	BatchStatusInactive  BatchStatus = "inactive"
	BatchStatusExpired   BatchStatus = "expired"
	BatchStatusRecalled  BatchStatus = "recalled"
	BatchStatusExhausted BatchStatus = "exhausted"
)

var AllBatchStatuses = []BatchStatus{
	BatchStatusActive, BatchStatusInactive, BatchStatusExpired,
	BatchStatusRecalled, BatchStatusExhausted,
}

func (b BatchStatus) String() string { return string(b) }
func (b BatchStatus) Valid() bool    { return contains(AllBatchStatuses, b) }

// Sellable reports whether the POS may consume from this batch.
func (b BatchStatus) Sellable() bool { return b == BatchStatusActive }

func ParseBatchStatus(s string) (BatchStatus, error) { return ParseEnum(AllBatchStatuses, s) }
func (b *BatchStatus) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllBatchStatuses, b)
}

//  Stock movement type

type StockMovementType string

const (
	StockMovePurchase      StockMovementType = "purchase"
	StockMoveSale          StockMovementType = "sale"
	StockMoveSaleReturn    StockMovementType = "sale_return"
	StockMoveTransferIn    StockMovementType = "transfer_in"
	StockMoveTransferOut   StockMovementType = "transfer_out"
	StockMoveAdjustmentIn  StockMovementType = "adjustment_in"
	StockMoveAdjustmentOut StockMovementType = "adjustment_out"
	StockMoveDamage        StockMovementType = "damage"
	StockMoveExpiry        StockMovementType = "expiry"
)

var AllStockMovementTypes = []StockMovementType{
	StockMovePurchase, StockMoveSale, StockMoveSaleReturn,
	StockMoveTransferIn, StockMoveTransferOut,
	StockMoveAdjustmentIn, StockMoveAdjustmentOut,
	StockMoveDamage, StockMoveExpiry,
}

func (m StockMovementType) String() string { return string(m) }
func (m StockMovementType) Valid() bool    { return contains(AllStockMovementTypes, m) }

// IsInbound reports whether the movement increases stock.
func (m StockMovementType) IsInbound() bool {
	switch m {
	case StockMovePurchase, StockMoveSaleReturn, StockMoveTransferIn, StockMoveAdjustmentIn:
		return true
	}
	return false
}

func ParseStockMovementType(s string) (StockMovementType, error) {
	return ParseEnum(AllStockMovementTypes, s)
}
func (m *StockMovementType) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllStockMovementTypes, m)
}

//  Warehouse transfer state

type TransferState string

const (
	TransferStateDraft      TransferState = "draft"
	TransferStateDispatched TransferState = "dispatched"
	TransferStateInTransit  TransferState = "in_transit"
	TransferStateReceived   TransferState = "received"
	TransferStateCancelled  TransferState = "cancelled"
)

var AllTransferStates = []TransferState{
	TransferStateDraft, TransferStateDispatched, TransferStateInTransit,
	TransferStateReceived, TransferStateCancelled,
}

func (t TransferState) String() string { return string(t) }
func (t TransferState) Valid() bool    { return contains(AllTransferStates, t) }

func ParseTransferState(s string) (TransferState, error) { return ParseEnum(AllTransferStates, s) }
func (t *TransferState) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllTransferStates, t)
}

//  Ledger account type / normal side

type AccountType string

const (
	AccountAsset     AccountType = "asset"
	AccountLiability AccountType = "liability"
	AccountEquity    AccountType = "equity"
	AccountRevenue   AccountType = "revenue"
	AccountExpense   AccountType = "expense"
)

var AllAccountTypes = []AccountType{
	AccountAsset, AccountLiability, AccountEquity, AccountRevenue, AccountExpense,
}

func (a AccountType) String() string { return string(a) }
func (a AccountType) Valid() bool    { return contains(AllAccountTypes, a) }

// NormalSide returns "debit" or "credit" for the account type. Used by
// ledger validation.
func (a AccountType) NormalSide() NormalSide {
	switch a {
	case AccountAsset, AccountExpense:
		return NormalSideDebit
	default:
		return NormalSideCredit
	}
}

func ParseAccountType(s string) (AccountType, error) { return ParseEnum(AllAccountTypes, s) }
func (a *AccountType) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllAccountTypes, a)
}

type NormalSide string

const (
	NormalSideDebit  NormalSide = "debit"
	NormalSideCredit NormalSide = "credit"
)

var AllNormalSides = []NormalSide{NormalSideDebit, NormalSideCredit}

func (n NormalSide) String() string { return string(n) }
func (n NormalSide) Valid() bool    { return contains(AllNormalSides, n) }

func ParseNormalSide(s string) (NormalSide, error) { return ParseEnum(AllNormalSides, s) }
func (n *NormalSide) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllNormalSides, n)
}

//  Journal status

type JournalStatus string

const (
	JournalStatusDraft    JournalStatus = "draft"
	JournalStatusPosted   JournalStatus = "posted"
	JournalStatusReversed JournalStatus = "reversed"
)

var AllJournalStatuses = []JournalStatus{
	JournalStatusDraft, JournalStatusPosted, JournalStatusReversed,
}

func (s JournalStatus) String() string { return string(s) }
func (s JournalStatus) Valid() bool    { return contains(AllJournalStatuses, s) }

func ParseJournalStatus(s string) (JournalStatus, error) { return ParseEnum(AllJournalStatuses, s) }
func (s *JournalStatus) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllJournalStatuses, s)
}

//  Target

type TargetType string

const (
	TargetTypeRevenue TargetType = "revenue"
	TargetTypeUnits   TargetType = "units"
	TargetTypeProfit  TargetType = "profit"
)

var AllTargetTypes = []TargetType{TargetTypeRevenue, TargetTypeUnits, TargetTypeProfit}

func (t TargetType) String() string { return string(t) }
func (t TargetType) Valid() bool    { return contains(AllTargetTypes, t) }

func ParseTargetType(s string) (TargetType, error) {
	return ParseEnum(AllTargetTypes, s)
}

func (t *TargetType) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllTargetTypes, t)
}

type TargetScope string

const (
	TargetScopeOrg    TargetScope = "org"
	TargetScopeBranch TargetScope = "branch"
)

var AllTargetScopes = []TargetScope{TargetScopeOrg, TargetScopeBranch}

func (t TargetScope) String() string {
	return string(t)
}

func (t TargetScope) Valid() bool {
	return contains(AllTargetScopes, t)
}

func ParseTargetScope(s string) (TargetScope, error) {
	return ParseEnum(AllTargetScopes, s)
}

func (t *TargetScope) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllTargetScopes, t)
}

type TargetPeriod string

const (
	TargetPeriodMonthly   TargetPeriod = "monthly"
	TargetPeriodQuarterly TargetPeriod = "quarterly"
	TargetPeriodYearly    TargetPeriod = "yearly"
)

var AllTargetPeriods = []TargetPeriod{TargetPeriodMonthly, TargetPeriodQuarterly, TargetPeriodYearly}

func (t TargetPeriod) String() string { return string(t) }
func (t TargetPeriod) Valid() bool    { return contains(AllTargetPeriods, t) }

func ParseTargetPeriod(s string) (TargetPeriod, error) { return ParseEnum(AllTargetPeriods, s) }
func (t *TargetPeriod) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllTargetPeriods, t)
}

//  Notification priority / scope

type NotifPriority string

const (
	NotifPriorityLow      NotifPriority = "low"
	NotifPriorityNormal   NotifPriority = "normal"
	NotifPriorityHigh     NotifPriority = "high"
	NotifPriorityCritical NotifPriority = "critical"
)

var AllNotifPriorities = []NotifPriority{
	NotifPriorityLow, NotifPriorityNormal, NotifPriorityHigh, NotifPriorityCritical,
}

func (p NotifPriority) String() string { return string(p) }
func (p NotifPriority) Valid() bool    { return contains(AllNotifPriorities, p) }

func ParseNotifPriority(s string) (NotifPriority, error) { 
	return ParseEnum(AllNotifPriorities, s) 
}

func (p *NotifPriority) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllNotifPriorities, p)
}

type NotifScope string

const (
	NotifScopeUser   NotifScope = "user"
	NotifScopeRole   NotifScope = "role"
	NotifScopeBranch NotifScope = "branch"
	NotifScopeOrg    NotifScope = "org"
)

var AllNotifScopes = []NotifScope{NotifScopeUser, NotifScopeRole, NotifScopeBranch, NotifScopeOrg}

func (s NotifScope) String() string { return string(s) }
func (s NotifScope) Valid() bool    { return contains(AllNotifScopes, s) }

func ParseNotifScope(s string) (NotifScope, error) { return ParseEnum(AllNotifScopes, s) }
func (s *NotifScope) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllNotifScopes, s)
}

//  Audit outcome

type AuditOutcome string

const (
	AuditOutcomeSuccess AuditOutcome = "success"
	AuditOutcomeFailure AuditOutcome = "failure"
)

var AllAuditOutcomes = []AuditOutcome{AuditOutcomeSuccess, AuditOutcomeFailure}

func (o AuditOutcome) String() string { return string(o) }
func (o AuditOutcome) Valid() bool    { return contains(AllAuditOutcomes, o) }

func ParseAuditOutcome(s string) (AuditOutcome, error) { return ParseEnum(AllAuditOutcomes, s) }
func (o *AuditOutcome) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllAuditOutcomes, o)
}

//  AI forecast

type ForecastType string

const (
	ForecastTypeDemand          ForecastType = "demand"
	ForecastTypeRestock         ForecastType = "restock"
	ForecastTypeBusinessSummary ForecastType = "business_summary"
	ForecastTypeProductMix      ForecastType = "product_mix"
)

var AllForecastTypes = []ForecastType{
	ForecastTypeDemand, ForecastTypeRestock,
	ForecastTypeBusinessSummary, ForecastTypeProductMix,
}

func (f ForecastType) String() string { return string(f) }
func (f ForecastType) Valid() bool    { return contains(AllForecastTypes, f) }

func ParseForecastType(s string) (ForecastType, error) { return ParseEnum(AllForecastTypes, s) }
func (f *ForecastType) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllForecastTypes, f)
}

//  Backup

type BackupType string

const (
	BackupTypeFull        BackupType = "full"
	BackupTypeIncremental BackupType = "incremental"
	BackupTypeWAL         BackupType = "wal"
)

var AllBackupTypes = []BackupType{BackupTypeFull, BackupTypeIncremental, BackupTypeWAL}

func (b BackupType) String() string { return string(b) }
func (b BackupType) Valid() bool    { return contains(AllBackupTypes, b) }

func ParseBackupType(s string) (BackupType, error) { return ParseEnum(AllBackupTypes, s) }

func (b *BackupType) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllBackupTypes, b)
}

type BackupStatus string

const (
	BackupStatusInProgress BackupStatus = "in_progress"
	BackupStatusSuccess    BackupStatus = "success"
	BackupStatusFailed     BackupStatus = "failed"
)

var AllBackupStatuses = []BackupStatus{
	BackupStatusInProgress, BackupStatusSuccess, BackupStatusFailed,
}

func (s BackupStatus) String() string { return string(s) }
func (s BackupStatus) Valid() bool    { return contains(AllBackupStatuses, s) }

func ParseBackupStatus(s string) (BackupStatus, error) { return ParseEnum(AllBackupStatuses, s) }

func (s *BackupStatus) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllBackupStatuses, s)
}

//  Discount type on coupons

type CouponDiscountType string

const (
	CouponDiscountPercent CouponDiscountType = "percent"
	CouponDiscountFlat    CouponDiscountType = "flat"
)

var AllCouponDiscountTypes = []CouponDiscountType{CouponDiscountPercent, CouponDiscountFlat}

func (c CouponDiscountType) String() string { return string(c) }
func (c CouponDiscountType) Valid() bool    { return contains(AllCouponDiscountTypes, c) }

func ParseCouponDiscountType(s string) (CouponDiscountType, error) {
	return ParseEnum(AllCouponDiscountTypes, s)
}
func (c *CouponDiscountType) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllCouponDiscountTypes, c)
}

//  Bank account type (users.user_bank_accounts.account_type)

type BankAccountType string

const (
	BankAccountBank   BankAccountType = "bank"
	BankAccountMobile BankAccountType = "mobile"
	BankAccountOther  BankAccountType = "other"
)

var AllBankAccountTypes = []BankAccountType{BankAccountBank, BankAccountMobile, BankAccountOther}

func (b BankAccountType) String() string { return string(b) }
func (b BankAccountType) Valid() bool    { return contains(AllBankAccountTypes, b) }

func ParseBankAccountType(s string) (BankAccountType, error) {
	return ParseEnum(AllBankAccountTypes, s)
}
func (b *BankAccountType) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllBankAccountTypes, b)
}

//  Session revoke reason

type SessionRevokeReason string

const (
	RevokeReasonLogout          SessionRevokeReason = "logout"
	RevokeReasonLogoutAll       SessionRevokeReason = "logout_all"
	RevokeReasonAdminForce      SessionRevokeReason = "admin_force_logout"
	RevokeReasonStatusChanged   SessionRevokeReason = "status_changed"
	RevokeReasonReuseDetected   SessionRevokeReason = "reuse_detected"
	RevokeReasonPasswordChanged SessionRevokeReason = "password_changed"
	RevokeReasonExpired         SessionRevokeReason = "expired"
)

var AllSessionRevokeReasons = []SessionRevokeReason{
	RevokeReasonLogout, RevokeReasonLogoutAll, RevokeReasonAdminForce,
	RevokeReasonStatusChanged, RevokeReasonReuseDetected,
	RevokeReasonPasswordChanged, RevokeReasonExpired,
}

func (r SessionRevokeReason) String() string { return string(r) }
func (r SessionRevokeReason) Valid() bool    { return contains(AllSessionRevokeReasons, r) }

func ParseSessionRevokeReason(s string) (SessionRevokeReason, error) {
	return ParseEnum(AllSessionRevokeReasons, s)
}
func (r *SessionRevokeReason) UnmarshalJSON(data []byte) error {
	return unmarshalEnumJSON(data, AllSessionRevokeReasons, r)
}

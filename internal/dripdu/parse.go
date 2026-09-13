package dripdu

import (
	"errors"
	"math/big"
	"regexp"
)

// flowPattern is the only accepted on-the-wire decimal form for flow_lph and
// rated_flow_lph: an integer part plus at most three fractional digits.
// Exponents, signs and scientific notation are intentionally rejected so
// "three decimal places" means exactly what it says.
var flowPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]{1,3})?$`)

var (
	// ErrFlowFormat means the value was not a plain decimal with at most
	// three digits after the decimal point.
	ErrFlowFormat = errors.New("flow_lph must be a decimal number with at most three decimal places")
	// ErrFlowRange means the value was 0, negative, or above 100.
	ErrFlowRange = errors.New("flow_lph must be greater than 0 and at most 100")
	// ErrRatedFormat is ErrFlowFormat for a rated_flow_lph value.
	ErrRatedFormat = errors.New("rated_flow_lph must be a decimal number with at most three decimal places")
	// ErrRatedRange is ErrFlowRange for a rated_flow_lph value.
	ErrRatedRange = errors.New("rated_flow_lph must be greater than 0 and at most 100")
	// ErrCapacityFormat is ErrFlowFormat for a pump_capacity_lph value.
	ErrCapacityFormat = errors.New("pump_capacity_lph must be a decimal number with at most three decimal places")
	// ErrCapacityRange means the pump capacity was 0 or negative; unlike a
	// measured flow it carries no 100 lph ceiling.
	ErrCapacityRange = errors.New("pump_capacity_lph must be greater than 0")
	// ErrDesignFlowFormat is ErrFlowFormat for a design_flow_lph value.
	ErrDesignFlowFormat = errors.New("design_flow_lph must be a decimal number with at most three decimal places")
	// ErrDesignFlowRange means the design flow was 0 or negative; an emitter
	// may draw more than 100 lph as long as it fits the pump.
	ErrDesignFlowRange = errors.New("design_flow_lph must be greater than 0")
)

// maxFlowThousandths is the 100 lph ceiling of flow_lph and rated_flow_lph,
// expressed in thousandths like the parsed values.
var maxFlowThousandths = big.NewInt(100000)

// ParseFlow parses one flow_lph value into an exact rational number.
//
// Accepted input is a plain decimal (one to three digits after the decimal
// point are allowed) satisfying 0 < flow <= 100. Values such as "0",
// "-1", "100.001", "0.0001" and "1e2" are rejected.
func ParseFlow(s string) (*big.Rat, error) {
	return parseDecimal(s, ErrFlowFormat, ErrFlowRange, maxFlowThousandths)
}

// ParseRatedFlow parses one rated_flow_lph value into an exact rational
// number. The accepted form and range are identical to ParseFlow; only the
// error messages name the rated field.
func ParseRatedFlow(s string) (*big.Rat, error) {
	return parseDecimal(s, ErrRatedFormat, ErrRatedRange, maxFlowThousandths)
}

// ParsePumpCapacity parses one pump_capacity_lph value into an exact
// rational number. The accepted decimal form is identical to ParseFlow, but
// the pump's available flow has no upper bound: it may serve any number of
// valve groups.
func ParsePumpCapacity(s string) (*big.Rat, error) {
	return parseDecimal(s, ErrCapacityFormat, ErrCapacityRange, nil)
}

// ParseDesignFlow parses one design_flow_lph value into an exact rational
// number. Like ParsePumpCapacity it enforces only the plain-decimal form and
// a strictly positive value; whether the flow fits the pump is a
// request-level check, not a property of the number itself.
func ParseDesignFlow(s string) (*big.Rat, error) {
	return parseDecimal(s, ErrDesignFlowFormat, ErrDesignFlowRange, nil)
}

// parseDecimal parses s as thousandths and enforces the range rule. A nil
// maxThousandths lifts the upper bound, leaving only the positivity check.
func parseDecimal(s string, formatErr, rangeErr error, maxThousandths *big.Int) (*big.Rat, error) {
	if !flowPattern.MatchString(s) {
		return nil, formatErr
	}

	intPart, fracPart := splitDecimal(s)

	// Value as exact thousandths: inputs carry at most three fractional
	// digits, so scaling by 1000 loses nothing and makes comparisons exact.
	whole := new(big.Int)
	if _, ok := whole.SetString(intPart, 10); !ok {
		return nil, formatErr
	}
	thousandths := new(big.Int).Mul(whole, big.NewInt(1000))
	if fracPart != "" {
		frac := new(big.Int)
		if _, ok := frac.SetString(fracPart, 10); !ok {
			return nil, formatErr
		}
		for i := len(fracPart); i < 3; i++ {
			frac.Mul(frac, big.NewInt(10))
		}
		thousandths.Add(thousandths, frac)
	}

	if thousandths.Sign() <= 0 {
		return nil, rangeErr
	}
	if maxThousandths != nil && thousandths.Cmp(maxThousandths) > 0 {
		return nil, rangeErr
	}

	return new(big.Rat).SetFrac(thousandths, big.NewInt(1000)), nil
}

func splitDecimal(s string) (whole, frac string) {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

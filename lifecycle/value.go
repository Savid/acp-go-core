package lifecycle

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
)

// decodeValue reads one JSON document as the value form every comparison in this
// extension runs on. Numbers are retained as their literals rather than collapsed
// into binary floating point: lifecycle value equality compares them as exact
// mathematical values, and a float64 cannot hold one.
func decodeValue(raw json.RawMessage) (any, bool) {
	reader := json.NewDecoder(bytes.NewReader(raw))
	reader.UseNumber()

	var value any
	if err := reader.Decode(&value); err != nil {
		return nil, false
	}

	return value, true
}

// valueEqual reports lifecycle value equality: two decoded values are equal when
// they are deeply equal, key order and insignificant whitespace are not
// differences, and numbers compare as exact mathematical values.
func valueEqual(left, right any) bool {
	switch value := left.(type) {
	case map[string]any:
		return objectEqual(value, right)
	case []any:
		return arrayEqual(value, right)
	case json.Number:
		other, ok := right.(json.Number)

		return ok && normalizedNumber(string(value)) == normalizedNumber(string(other))
	default:
		return left == right
	}
}

func objectEqual(left map[string]any, right any) bool {
	other, ok := right.(map[string]any)
	if !ok || len(left) != len(other) {
		return false
	}

	for key, value := range left {
		counterpart, present := other[key]
		if !present || !valueEqual(value, counterpart) {
			return false
		}
	}

	return true
}

func arrayEqual(left []any, right any) bool {
	other, ok := right.([]any)
	if !ok || len(left) != len(other) {
		return false
	}

	for index := range left {
		if !valueEqual(left[index], other[index]) {
			return false
		}
	}

	return true
}

// rawEqual compares two undecoded members under the same equality. A member
// nothing delivered decodes to nothing, so an absent member equals an absent one
// and differs from every present value.
func rawEqual(left, right json.RawMessage) bool {
	leftValue, _ := decodeValue(left)
	rightValue, _ := decodeValue(right)

	return valueEqual(leftValue, rightValue)
}

// normalizedNumber renders one JSON number lexeme as its normalized decimal
// form: the sign, the coefficient with leading and trailing zeros stripped, and
// the adjusted exponent that stripping left. Every zero normalizes to the same
// form, so -0 and 0 are one value. The rendering never materializes the number.
func normalizedNumber(lexeme string) string {
	mantissa, exponent := lexeme, ""
	if index := strings.IndexAny(lexeme, "eE"); index >= 0 {
		mantissa, exponent = lexeme[:index], lexeme[index+1:]
	}

	sign := ""
	if strings.HasPrefix(mantissa, "-") {
		sign, mantissa = "-", mantissa[1:]
	}

	integral, fraction := mantissa, ""
	if before, after, ok := strings.Cut(mantissa, "."); ok {
		integral, fraction = before, after
	}

	coefficient := strings.TrimLeft(integral+fraction, "0")

	digits := strings.TrimRight(coefficient, "0")
	if digits == "" {
		return "0"
	}

	var scale int64

	if exponent != "" {
		parsed, err := strconv.ParseInt(exponent, 10, 64)
		if err != nil {
			// An exponent past int64 names no number a frame can carry, so the
			// lexeme stands as its own normalized form.
			return lexeme
		}

		scale = parsed
	}

	adjusted := new(big.Int).SetInt64(scale)
	adjusted.Sub(adjusted, big.NewInt(int64(len(fraction)-(len(coefficient)-len(digits)))))

	return sign + digits + "e" + adjusted.String()
}

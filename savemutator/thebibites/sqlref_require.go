package thebibites

import "fmt"

func requireSQLRefString(ref SQLValueRef, value, field string) error {
	if value == "" {
		return fmt.Errorf("%s.%s requires %s", ref.Table, ref.Column, field)
	}
	return nil
}

func requireSQLRefFlag(ref SQLValueRef, ok bool, field string) error {
	if !ok {
		return fmt.Errorf("%s.%s requires %s", ref.Table, ref.Column, field)
	}
	return nil
}

func requireSQLRefEqual(ref SQLValueRef, field, got, want string) error {
	if got != want {
		return fmt.Errorf("%s.%s %s = %q, want %s", ref.Table, ref.Column, field, got, want)
	}
	return nil
}

func requireSQLRefValueType(ref SQLValueRef, want string) error {
	if err := requireSQLRefString(ref, ref.ValueType, "value_type"); err != nil {
		return err
	}
	return requireSQLRefEqual(ref, "value_type", ref.ValueType, want)
}

func sqlRefColumnValue(ref SQLValueRef, values map[string]string) (string, error) {
	value, ok := values[ref.Column]
	if !ok {
		return "", unsupportedSQLValueRef(ref)
	}
	return value, nil
}

func zoneIDGuards(ref SQLValueRef, zoneIndex int) []Guard {
	if !ref.HasZoneID {
		return nil
	}
	return []Guard{Require(fmt.Sprintf("zones[%d].id", zoneIndex), ref.ZoneID)}
}

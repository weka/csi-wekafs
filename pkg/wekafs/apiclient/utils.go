package apiclient

import (
	"github.com/rs/zerolog/log"
	"reflect"
)

// ObjectsAreEqual returns true if both ApiObject have same immutable fields (other fields and nil fields are disregarded)
func ObjectsAreEqual(o1 ApiObject, o2 ApiObject) bool {
	if reflect.TypeOf(o1) != reflect.TypeOf(o2) {
		return false
	}
	ref := reflect.ValueOf(o1)
	oth := reflect.ValueOf(o2)
	for _, field := range o1.getImmutableFields() {
		qval := reflect.Indirect(ref).FieldByName(field)
		val := reflect.Indirect(oth).FieldByName(field)
		if !qval.IsZero() {
			if !reflect.DeepEqual(qval.Interface(), val.Interface()) {
				return false
			}
		}
	}
	return true
}

// ObjectRequestHasRequiredFields returns true if all mandatory fields of object in API request are filled in
func ObjectRequestHasRequiredFields(o ApiObjectRequest) bool {
	ref := reflect.ValueOf(o)
	var missingFields []string
	for _, field := range o.getRequiredFields() {
		if reflect.Indirect(ref).FieldByName(field).IsZero() {
			missingFields = append(missingFields, field)
		}
	}
	if len(missingFields) > 0 {
		log.Error().Strs("missing_fileds", missingFields).Msg("Object is missing mandatory fields")
		return false
	}
	return true
}

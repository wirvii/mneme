package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

func normalizeContractText(v string) string {
	v = strings.ReplaceAll(v, "\r\n", "\n")
	v = strings.ReplaceAll(v, "\r", "\n")
	return strings.TrimSpace(v)
}
func appendField(b *bytes.Buffer, key, value string) {
	value = normalizeContractText(value)
	b.WriteString(key)
	b.WriteByte(0x1f)
	b.WriteString(strconv.Itoa(len([]byte(value))))
	b.WriteByte(0x1f)
	b.WriteString(value)
	b.WriteByte(0x1e)
}
func normalizedUnique(values []string) []string {
	set := map[string]bool{}
	for _, v := range values {
		v = normalizeContractText(v)
		if !set[v] {
			set[v] = true
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
func appendList(b *bytes.Buffer, key string, values []string) {
	values = normalizedUnique(values)
	b.WriteString(key)
	b.WriteByte(0x1f)
	b.WriteString(strconv.Itoa(len(values)))
	b.WriteByte(0x1e)
	for _, v := range values {
		appendField(b, "item", v)
	}
}

// ContractHashInput returns the canonical bytes whose exact content defines completion.
func ContractHashInput(c WorkContract, criteria []WorkCriterion, constraints []WorkConstraint) []byte {
	var b bytes.Buffer
	b.WriteString("mneme/execution-contract")
	b.WriteByte(0x1e)
	b.WriteString("v1")
	b.WriteByte(0x1e)
	appendField(&b, "goal", c.Goal)
	appendList(&b, "scope", c.Scope)
	criteria = append([]WorkCriterion(nil), criteria...)
	sort.Slice(criteria, func(i, j int) bool { return criteria[i].Key < criteria[j].Key })
	b.WriteString("criteria")
	b.WriteByte(0x1f)
	b.WriteString(strconv.Itoa(len(criteria)))
	b.WriteByte(0x1e)
	for _, v := range criteria {
		appendField(&b, "k", v.Key)
		appendField(&b, "v", v.Declaration)
	}
	constraints = append([]WorkConstraint(nil), constraints...)
	sort.Slice(constraints, func(i, j int) bool { return constraints[i].Key < constraints[j].Key })
	b.WriteString("constraints")
	b.WriteByte(0x1f)
	b.WriteString(strconv.Itoa(len(constraints)))
	b.WriteByte(0x1e)
	for _, v := range constraints {
		appendField(&b, "k", v.Key)
		appendField(&b, "v", v.Text)
	}
	verification := make([]string, len(c.Verification))
	for i, v := range c.Verification {
		verification[i] = string(v)
	}
	appendList(&b, "verification", verification)
	appendField(&b, "development_method", string(c.DevelopmentMethod))
	return b.Bytes()
}

// ContractHash returns lowercase hexadecimal SHA-256 of ContractHashInput.
func ContractHash(c WorkContract, criteria []WorkCriterion, constraints []WorkConstraint) string {
	sum := sha256.Sum256(ContractHashInput(c, criteria, constraints))
	return hex.EncodeToString(sum[:])
}

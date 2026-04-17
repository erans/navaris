package main

import (
	"reflect"
	"testing"
)

func TestParseEnvFlag_SingleKV(t *testing.T) {
	got, err := parseEnvFlags([]string{"FOO=bar"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"FOO": "bar"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseEnvFlag_MultipleKV(t *testing.T) {
	got, err := parseEnvFlags([]string{"A=1", "B=two", "C=hello world"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "1", "B": "two", "C": "hello world"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseEnvFlag_RejectsMissingEquals(t *testing.T) {
	_, err := parseEnvFlags([]string{"NOEQUALS"})
	if err == nil {
		t.Error("expected error for missing '=' separator")
	}
}

func TestParseEnvFlag_RejectsEmptyKey(t *testing.T) {
	_, err := parseEnvFlags([]string{"=value"})
	if err == nil {
		t.Error("expected error for empty key")
	}
}

func TestParseEnvFlag_AllowsEqualsInValue(t *testing.T) {
	got, err := parseEnvFlags([]string{"URL=http://example.com?a=1"})
	if err != nil {
		t.Fatal(err)
	}
	if got["URL"] != "http://example.com?a=1" {
		t.Errorf("got %q, want full value with embedded =", got["URL"])
	}
}

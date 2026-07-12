package prom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestQueryVector 验证 vector 结果按 instance 解析。
func TestQueryVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"instance":"10.0.0.1:9100"},"value":[1700000000,"93.5"]},
			{"metric":{"instance":"10.0.0.2:9100"},"value":[1700000000,"12.0"]}
		]}}`))
	}))
	defer srv.Close()

	values, err := New(srv.URL).QueryVector(context.Background(), "up")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(values) != 2 || values["10.0.0.1:9100"] != 93.5 {
		t.Fatalf("解析结果不符: %+v", values)
	}
}

// TestQueryVectorErrors 验证查询失败与非 vector 结果报错。
func TestQueryVectorErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","error":"bad query"}`))
	}))
	defer srv.Close()
	if _, err := New(srv.URL).QueryVector(context.Background(), "x"); err == nil {
		t.Fatal("期望查询失败报错")
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer srv2.Close()
	if _, err := New(srv2.URL).QueryVector(context.Background(), "x"); err == nil {
		t.Fatal("期望非 vector 结果报错")
	}
}

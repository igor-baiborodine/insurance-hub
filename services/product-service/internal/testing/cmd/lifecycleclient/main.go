// Command lifecycleclient issues concurrent HTTP and gRPC reads against a disposable Product Pod.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
)

type result struct {
	HTTPStatus int    `json:"httpStatus"`
	HTTPFailed bool   `json:"httpFailed"`
	GRPCCode   string `json:"grpcCode"`
	GRPCItem   string `json:"grpcItem"`
}

func main() {
	httpEndpoint := flag.String("http", "", "disposable Pod HTTP endpoint")
	grpcEndpoint := flag.String("grpc", "", "disposable Pod gRPC endpoint")
	flag.Parse()
	if *httpEndpoint == "" || *grpcEndpoint == "" {
		fmt.Fprintln(os.Stderr, "both endpoints are required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	connection, err := grpcgo.NewClient(*grpcEndpoint,
		grpcgo.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintln(os.Stderr, "gRPC connection could not be created")
		os.Exit(2)
	}
	defer func() { _ = connection.Close() }()
	client := productv1.NewProductServiceClient(connection)
	out := result{}
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet,
			*httpEndpoint+"/products", nil)
		if requestErr != nil {
			out.HTTPFailed = true
			return
		}
		response, callErr := (&http.Client{Timeout: 24 * time.Second}).Do(request)
		if callErr != nil {
			out.HTTPFailed = true
			return
		}
		defer func() { _ = response.Body.Close() }()
		_, readErr := io.Copy(io.Discard, response.Body)
		out.HTTPStatus = response.StatusCode
		out.HTTPFailed = readErr != nil
	}()
	go func() {
		defer group.Done()
		response, callErr := client.GetProduct(ctx,
			&productv1.GetProductRequest{Code: "CAR"})
		out.GRPCCode = status.Code(callErr).String()
		if callErr == nil {
			out.GRPCItem = response.GetProduct().GetCode()
		}
	}()
	fmt.Println("started")
	group.Wait()
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "result could not be written")
		os.Exit(1)
	}
}

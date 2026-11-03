package discovery_test

import (
	"context"
	"fmt"

	"github.com/Manu343726/hammer/discovery"
	"github.com/Manu343726/hammer/internal/testcontract"
	testcontractv1connect "github.com/Manu343726/hammer/internal/testcontract/testcontractv1/testcontractv1connect"
)

// Describing a service is how a caller learns a contract it was never compiled against: the schema names
// every method and gives the message types to build a request with, so an invocation can follow without
// any generated code for the endpoint at the other end.
func ExampleClient_DescribeService() {
	path, handler := testcontractv1connect.NewApiInvokerServiceHandler(&testInvoker{})
	server, err := testcontract.Serve(testcontract.Mount{
		Name:    testcontractv1connect.ApiInvokerServiceName,
		Path:    path,
		Handler: handler,
	})
	if err != nil {
		panic(err)
	}
	defer func() { _ = server.Close() }()

	client := discovery.New(server.Endpoint())
	schema, err := client.DescribeService(context.Background(), testcontractv1connect.ApiInvokerServiceName)
	if err != nil {
		panic(err)
	}

	fmt.Println(schema.Name)
	for _, method := range schema.Methods {
		fmt.Printf("%s: %s -> %s\n", method.Name, method.Input.FullName(), method.Output.FullName())
	}

	// Output:
	// testcontract.v1.ApiInvokerService
	// InvokeApi: testcontract.v1.InvokeApiRequest -> testcontract.v1.InvokeApiResponse
}

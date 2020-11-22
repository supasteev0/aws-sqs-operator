module github.com/supasteev0/aws-sqs-operator

go 1.13

require (
	github.com/aws/aws-sdk-go v1.35.33
	github.com/go-logr/logr v0.1.0
	github.com/onsi/ginkgo v1.12.1
	github.com/onsi/gomega v1.10.1
	github.com/prometheus/client_golang v1.0.0
	k8s.io/apimachinery v0.18.8
	k8s.io/client-go v0.18.6
	sigs.k8s.io/controller-runtime v0.6.3
	sigs.k8s.io/kind v0.9.0 // indirect
)

# aws-sqs-operator

## Overview

Kubernetes operator to manage AWS SQS queues.

This operator can manage SQS queues (create/read/update/delete) in your AWS account, via a Sqs CRD.

You can configure the AWS region where the SQS queue will be created (mandatory) and the MaximumMessageSize attribute of the SQS queue (optional).

The operator will also display the url and number of messages in SQS queues via kubectl get command.

## Requirements

* Kubernetes server 1.16.x
* AWS account with necessary permissions to manage SQS queues
* Prometheus
* Kustomize

## Installation

By default, the operator will be deployed in the 'default' namespace.

You can change the destination namespace by editing the 'namespace' key in config/default/kustomization.yaml.

To deploy the operator, you can use the following command:  

```bash
make deploy
```

All deployments manifests are managed with kustomize, the base kustomize folder can be found [here](config/default).

You can also deploy the manifests via kustomize and kubectl with teh following command: 

```bash
$ kustomize build config/default | kubectl apply -f -
```

### AWS permissions

To allow the operator to access AWS resources, you should create an IAM role with [IRSA](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html)

Here is a sample code to create an IRSA compatible IAM role with Terraform: 

```hcl-terraform
resource "aws_iam_role" "sqs-operator" {
  name = "sqs-operator"
  path = "/"

  assume_role_policy = <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "AllowMyCluster",
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::ACCCOUNT_ID:oidc-provider/oidc.eks.eu-west-1.amazonaws.com/id/CLUSTER_ID"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "oidc.eks.eu-west-1.amazonaws.com/id/CLUSTER_ID:sub": "system:serviceaccount:default:sqs-operator"
        }
      }
    }
  ]
}
EOF
}

resource "aws_iam_policy" "sqs-operator" {
  name        = "sqs-operator"
  path        = "/"
  description = "Allow access to SQS queues"
  policy      = <<EOF
{
   "Version": "2012-10-17",
   "Statement": [
     {
      "Effect": "Allow",
      "Action": [
            "sqs:ListQueues",
            "sqs:GetQueueUrl",
            "sqs:DeleteQueue",
            "sqs:GetQueueAttributes",
            "sqs:CreateQueue"
      ],
      "Resource": "*"
     }
   ]
}
EOF
}

resource "aws_iam_role_policy_attachment" "sqs-operator" {
  policy_arn = aws_iam_policy.sqs-operator.arn
  role       = aws_iam_role.sqs-operator.name
}
``` 

Don't forget to change the namespace in the role policy condition if you deployed the operator in another namespace.

Once the IAM role has been created, you can add the `eks.amazonaws.com/role-arn` annotation to the `aws-sqs-operator-manager` serviceaccount: 

```yaml
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::ACCCOUNT_ID:role/sqs-operator
```

Example: 

```bash
$ kubectl annotate sa aws-sqs-operator-manager eks.amazonaws.com/role-arn='arn:aws:iam::ACCCOUNT_ID:role/sqs-operator'
```

If you feel very lazy or if you are testing the operator in a dev environment, you can just use your AWS credentials by creating a secret called `aws-secret` with AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY values: 

```bash
$ kubectl create secret generic aws-secret --from-literal='AWS_ACCESS_KEY_ID=xxxx' \
--from-literal='AWS_SECRET_ACCESS_KEY=xxxx'
```

**Note: NEVER DO THAT IN PRODUCTION !**

### Reconcile frequency

By default the operator will requeue all requests every 30 seconds.

This will allow the operator to poll for changes in AWS SQS queues.

You can configure this value by settings a RECONCILE_FREQUENCY_SECONDS environment variable in the deployment.

Example: 

```yaml
...
env:
- name: RECONCILE_FREQUENCY_SECONDS
  value: "10"
```


## Usage

### Creating a queue

The operator will watch all namespaces, so you can create your queues in any namespace.

To create an SQS queue, you have to create a Sqs object.

Here is a sample manifest that will create a queue called _my-super-queue_ in eu-west-1, with a maximum message size of 2 Kb : 

```yaml
apiVersion: queuing.aws.artifakt.io/v1alpha1
kind: Sqs
metadata:
  name: my-super-queue
spec:
  region: eu-west-1
  maxMessageSize: 2048
```

You can find some sample manifests in the [samples](config/samples) folder

### Posting a message in a queue

Example:

```bash
$ aws sqs send-message --queue-url https://sqs.eu-west-1.amazonaws.com/xxxxxxxxx/my-super-queue --message-body "My super message" --delay-seconds 0
```

### View SQS queues

To view your SQS queues, you can use the `kubectl get` command: 

```bash
$ kubectl get sqs
NAME       AGE   URL                                                         MESSAGES
sqs-test   34m   https://sqs.eu-west-1.amazonaws.com/671833555281/sqs-test   0
test2      26m   https://sqs.eu-west-1.amazonaws.com/671833555281/test2      0
```

The operator will display the URL and number of visible messages for each queue.


## Operator logic

This Kubernetes operator has been created with the [operator framework SDK](https://sdk.operatorframework.io) and Golang 1.15.1.

### CRD

Sqs CRD configuration is managed in [sqs_types.go](api/v1alpha1/sqs_types.go).

The MaxMessageSize property in the SqsSpec struct has additional `+kubebuilder:validation` annotations to make the property optional, and set minimum and maximum allowed values.

The Sqs struct has additional `+kubebuilder:printcolumn` annotations to generate the additionalPrinterColumns config in the CRD.

This config is used to display extra columns when executing kubectl get command.

### main.go

[main.go](main.go) is the entrypoint of the application. It has been generated with  the `operator-sdk init` command.

Following features were added : 

* metrics registry and custom metric initialization
* reconcile frequency configuration via RECONCILE_FREQUENCY_SECONDS environment variable

### controller

The [sqs controller](controllers/sqs_controller.go) handles Sqs resources reconciliation.

Its main goal is to create and delete Sqs resources, as well as updating their Status when an AWS SQS queue is updated.

 
It will also create, update and delete AWS SQS queues when Sqs resources change.

The SQS queues are deleted through the SqsReconciler finalizer, so the Sqs resource can only be deleted if the AWS SQS queue has successfully been deleted.  

 
As the controller can manage SQS queues in different AWS regions, it contains a `AwsSessions` map, in which AWS session objects will be initialized when needed.

This avoid creating multiple AWS sessions for the same region every time the reconciliation runs.


In order to update Sqs resources when AWS SQS queues changes occur, the Reconcile always returns ctrl.Result{RequeueAfter: r.RequeueAfterDuration}

This behavior will ensure that SQS queues are regularly polled and Sqs resources updated accordingly.

## Metrics

The operator exposes metric on port 8080, path /metrics.

A serviceMonitor is deployed to allow Prometheus to gather the metrics.

Default kubernetes controller metrics are exposed as well as a custom metric: 

`sqs_total_queues`: contains the total number of sqs queues created across all namespaces 

## CI

A sample [.gitlab-ci.yml](.gitlab-ci.yml) is provided in this repository as an example to build the project via a CI pipeline in Gitlab.

This sample configuration will: 

* build the application
* test the application
* build a docker image on a custom branch (manual)
* build and push the docker image on master branch
* tag and push the docker image when tagging the repository

## Development

### Prerequisites
* golang 1.15
* docker (used for creating container images, etc.)
* kind (optional)

### Running the operator

Example commands to build and run the operator:

```bash
$ IMG=sbressey/aws-sqs-operator:XXX
$ make docker-build
$ make docker-push-kind
$ make deploy
```

IMG variable must be changed every time in order to make sure kind will use the most recent Docker image. 

A [skaffold](https://skaffold.dev/) configuration file is also present because I find this tool quite useful to avoid executing commands every time you update the code and want to test it.

I used it at the beginning to push the image to Dockerhub, but, as my bandwidth is limited, I then switched to using kind (awesome tool BTW !).

I haven't configured skaffold to automatically push the image to kind, but it might be possible.  

## Time spent

I have spent around 16 hours to create this operator.
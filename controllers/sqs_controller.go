/*
Copyright 2020 Steven Bressey.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	queuingv1alpha1 "github.com/supasteev0/aws-sqs-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strconv"
	"time"
)

const (
	sqsFinalizer          = "finalizer.queuing.aws.artifakt.io"
	defaultMaxMessageSize = 262144 // AWS SQS MaximumMessageSize attribute default value is 262,144 (256 KiB).
)

// SqsReconciler reconciles a Sqs object
type SqsReconciler struct {
	client.Client
	Log                  logr.Logger
	Scheme               *runtime.Scheme
	AwsSessions          map[string]*session.Session
	RequeueAfterDuration time.Duration
	MetricsList          map[string]prometheus.Metric
}

// +kubebuilder:rbac:groups=queuing.aws.artifakt.io,resources=sqs-queues,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=queuing.aws.artifakt.io,resources=sqs-queues/status,verbs=get;update;patch

// Reconcile updates Sqs crds and AWS SQS queues
func (r *SqsReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("sqs", req.NamespacedName)
	log.Info("Reconciling Sqs")

	// Update metric
	queueList := &queuingv1alpha1.SqsList{}
	if err := r.List(ctx,queueList); err != nil {
		log.Error(err, "Failed to list Sqs")
	}
	// decrease total queues metric counter
	r.MetricsList["sqs_total_queues"].(prometheus.Gauge).Set(float64(len(queueList.Items)))

	// Fetch the Sqs instance
	queue := &queuingv1alpha1.Sqs{}
	if err := r.Get(ctx, req.NamespacedName, queue); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Sqs resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object
		log.Error(err, "Failed to get Sqs")
		return ctrl.Result{}, err
	}

	// Create AWS session if not already created
	if r.AwsSessions[queue.Spec.Region] == nil {
		sess, err := session.NewSession(&aws.Config{
			Region: &queue.Spec.Region,
		})
		if err != nil {
			log.Error(err, "error creating AWS session for region", "region", queue.Spec.Region)
			return ctrl.Result{Requeue: false}, err
		}
		r.AwsSessions[queue.Spec.Region] = sess
	}

	//===================================================
	// Create

	// Check if SQS queue already exist, if not create a new one
	url, err := getQueueURL(r.AwsSessions[queue.Spec.Region], *queue)
	if err != nil {
		log.Error(err, "error getting SQS queue from AWS")
		return ctrl.Result{Requeue: true}, err
	}

	if url == "" {
		// Create queue
		if err := createQueue(r.AwsSessions[queue.Spec.Region], *queue); err != nil {
			log.Error(err, "error creating SQS queue in region", "region", queue.Spec.Region)
			return ctrl.Result{Requeue: true}, err
		}

		log.Info("Successfully created SQS queue", "name", queue.Name)
		// SQS queue created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	}

	//===================================================
	// Update queue status

	// Update status.URL if needed
	if !reflect.DeepEqual(url, queue.Status.URL) {
		queue.Status.URL = url
		err := r.Status().Update(ctx, queue)
		log.Info("Updating Sqs status with url", "url", url)
		if err != nil {
			log.Error(err, "Failed to update Sqs url status")
			return ctrl.Result{Requeue: true}, err
		}
	}

	// Update VisibleMessages if needed
	visibleMessages, err := getQueueVisibleMessage(r.AwsSessions[queue.Spec.Region], *queue, log)
	if err != nil {
		log.Error(err, "Failed to get SQS queue ApproximateNumberOfMessages")
		return ctrl.Result{Requeue: true}, err
	}
	if !reflect.DeepEqual(visibleMessages, queue.Status.VisibleMessages) {
		queue.Status.VisibleMessages = visibleMessages
		err := r.Status().Update(ctx, queue)
		log.Info("Updating Sqs visibleMessages with number", "visibleMessages", visibleMessages)
		if err != nil {
			log.Error(err, "Failed to update Sqs visibleMessages status")
			return ctrl.Result{Requeue: true}, err
		}
	}

	//===================================================
	// Update SQS queue

	// Update MaxMessageSize if needed
	currentMaxMessageSize, err := getQueueMaxMessageSize(r.AwsSessions[queue.Spec.Region], *queue, log)
	if err != nil {
		log.Error(err, "Failed to get SQS queue MaximumMessageSize")
		return ctrl.Result{Requeue: true}, err
	}

	wantedSize := defaultMaxMessageSize
	if queue.Spec.MaxMessageSize != nil {
		wantedSize = *queue.Spec.MaxMessageSize
	}

	if !reflect.DeepEqual(currentMaxMessageSize, wantedSize) {
		if err := setQueueAttribute(r.AwsSessions[queue.Spec.Region], *queue, "MaximumMessageSize", strconv.Itoa(wantedSize), log); err != nil {
			log.Error(err, "Failed to set SQS queue MaximumMessageSize")
			return ctrl.Result{Requeue: true}, err
		}
	}

	//===================================================
	// Delete

	// Check if the queue instance is marked to be deleted
	isSqsMarkedToBeDeleted := queue.GetDeletionTimestamp() != nil

	if isSqsMarkedToBeDeleted {
		if contains(queue.GetFinalizers(), sqsFinalizer) {
			if err := r.finalizeSqs(log, queue, r.AwsSessions[queue.Spec.Region]); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(queue, sqsFinalizer)
			err := r.Update(ctx, queue)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer for this CR
	if !contains(queue.GetFinalizers(), sqsFinalizer) {
		if err := r.addFinalizer(log, queue); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: r.RequeueAfterDuration}, nil
}

// SetupWithManager register controller with manager
func (r *SqsReconciler) SetupWithManager(mgr ctrl.Manager) error {

	return ctrl.NewControllerManagedBy(mgr).
		For(&queuingv1alpha1.Sqs{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 2,
		}).
		Complete(r)
}

// finalizeSqs deletes SQS queue from AWS
func (r *SqsReconciler) finalizeSqs(log logr.Logger, m *queuingv1alpha1.Sqs, sess *session.Session) error {

	// Delete SQS queue
	if err := deleteQueue(sess, *m); err != nil {
		log.Error(err, "Error while deleting SQS queue")
	}

	log.Info("Successfully deleted SQS queue")
	return nil
}

// addFinalizer adds finalizer to Sqs instance
func (r *SqsReconciler) addFinalizer(log logr.Logger, m *queuingv1alpha1.Sqs) error {
	controllerutil.AddFinalizer(m, sqsFinalizer)

	// Update CR
	err := r.Update(context.TODO(), m)
	if err != nil {
		log.Error(err, "Failed to update Sqs with finalizer")
		return err
	}
	return nil
}

//================================================================ GET FUNCTIONS
// getQueueAttribute returns a given attribute for the SQS queue
func getQueueAttribute(sess *session.Session, queue queuingv1alpha1.Sqs, attributeName string) (string, error) {
	svc := sqs.New(sess)
	//log.Info("Getting queue attribute", "attribute", attributeName)
	input := &sqs.GetQueueAttributesInput{
		QueueUrl: &queue.Status.URL,
		AttributeNames: []*string{
			aws.String(attributeName),
		},
	}
	result, err := svc.GetQueueAttributes(input)
	if err != nil {
		return "", err
	}

	return *result.Attributes[attributeName], nil
}

// getQueueVisibleMessage returns the number of visible messages in the SQS queue
func getQueueVisibleMessage(sess *session.Session, queue queuingv1alpha1.Sqs, log logr.Logger) (int32, error) {
	attributeValue, err := getQueueAttribute(sess, queue, "ApproximateNumberOfMessages")
	if err != nil {
		return 0, err
	}

	visibleMessages, _ := strconv.ParseInt(attributeValue, 10, 32)
	log.Info("Found " + attributeValue + " visible messages")
	return int32(visibleMessages), nil
}

// getQueueMaxMessageSize returns the current MaximumMessageSize configuration of the SQS queue
func getQueueMaxMessageSize(sess *session.Session, queue queuingv1alpha1.Sqs, log logr.Logger) (int, error) {
	attributeValue, err := getQueueAttribute(sess, queue, "MaximumMessageSize")
	if err != nil {
		return 0, err
	}

	maxMessageSize, _ := strconv.ParseInt(attributeValue, 10, 32)
	//log.Info("Current MaximumMessageSize: " + attributeValue)
	return int(maxMessageSize), nil
}

// GetQueueURL returns SQS queue url
func getQueueURL(sess *session.Session, queue queuingv1alpha1.Sqs) (string, error) {
	svcSQS := sqs.New(sess)
	input := sqs.GetQueueUrlInput{QueueName: &queue.Name}
	result, err := svcSQS.GetQueueUrl(&input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() == sqs.ErrCodeQueueDoesNotExist {
				return "", nil
			}
		}
		return "", err
	}

	return *result.QueueUrl, nil
}

//================================================================ SET FUNCTIONS
// setQueueAttribute sets given attribute value in SQS queue
func setQueueAttribute(sess *session.Session, queue queuingv1alpha1.Sqs, attributeName string, attributeValue string, log logr.Logger) error {
	svc := sqs.New(sess)
	log.Info("Setting queue attribute", "attribute", attributeName, "value", attributeValue)
	input := &sqs.SetQueueAttributesInput{
		QueueUrl: aws.String(queue.Status.URL),
		Attributes: map[string]*string{
			attributeName: aws.String(attributeValue),
		},
	}
	_, err := svc.SetQueueAttributes(input)
	if err != nil {
		return err
	}

	return nil
}

//================================================================ CREATE/DELETE FUNCTIONS
// createQueue creates a new SQS queue
func createQueue(sess *session.Session, queue queuingv1alpha1.Sqs) error {
	svc := sqs.New(sess)

	size := strconv.Itoa(defaultMaxMessageSize)
	if queue.Spec.MaxMessageSize != nil {
		size = strconv.Itoa(*queue.Spec.MaxMessageSize)
	}

	_, err := svc.CreateQueue(&sqs.CreateQueueInput{
		QueueName: aws.String(queue.Name),
		Attributes: map[string]*string{
			"MaximumMessageSize": aws.String(size),
		},
	})
	if err != nil {
		return err
	}

	return nil
}

// deleteQueue deletes SQS queue from AWS
func deleteQueue(sess *session.Session, queue queuingv1alpha1.Sqs) error {
	svc := sqs.New(sess)

	_, err := svc.DeleteQueue(&sqs.DeleteQueueInput{
		QueueUrl: aws.String(queue.Status.URL),
	})
	if err != nil {
		return err
	}

	return nil
}

// contains check if a list contains a string
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

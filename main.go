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

package main

import (
	"flag"
	"fmt"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/prometheus/client_golang/prometheus"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	queuingv1alpha1 "github.com/supasteev0/aws-sqs-operator/api/v1alpha1"
	"github.com/supasteev0/aws-sqs-operator/controllers"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	totalQueuesCount = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "sqs_total_queues",
            Help: "Total number of sqs queues",
        },
    )
)

const (
	defaultRequeueAfterDuration time.Duration = 30 * time.Second
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(queuingv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme

	metrics.Registry.MustRegister(totalQueuesCount)
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		Port:               9443,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   "29f12e3b.aws.artifakt.io",
		Namespace:          "",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// reconcile frequency
	requeueAfterDuration := defaultRequeueAfterDuration
	if os.Getenv("RECONCILE_FREQUENCY_SECONDS") != "" {
		value, err := strconv.Atoi(os.Getenv("RECONCILE_FREQUENCY_SECONDS"))
		if err != nil {
			setupLog.Error(err, "RECONCILE_FREQUENCY_SECONDS environment variable is not a valid integer")
			os.Exit(1)
		}
		requeueAfterDuration = time.Duration(value) * time.Second
	}

	setupLog.Info("Configuring watch frequency to " + fmt.Sprintf("%d seconds", int64(requeueAfterDuration.Seconds())))

	// send metrics list to controller - might be a better way to retrieve metric from controller though
	metricsList := make(map[string]prometheus.Metric)
	metricsList["sqs_total_queues"] = totalQueuesCount

	if err = (&controllers.SqsReconciler{
		Client:               mgr.GetClient(),
		Log:                  ctrl.Log.WithName("controllers").WithName("Sqs"),
		Scheme:               mgr.GetScheme(),
		AwsSessions:          make(map[string]*session.Session),
		RequeueAfterDuration: requeueAfterDuration,
		MetricsList:          metricsList,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Sqs")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

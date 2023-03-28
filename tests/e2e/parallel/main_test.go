package parallel_e2e

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"testing"

	compv1alpha1 "github.com/ComplianceAsCode/compliance-operator/pkg/apis/compliance/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ComplianceAsCode/compliance-operator/tests/e2e/framework"
	"k8s.io/apimachinery/pkg/types"
)

var brokenContentImagePath string
var contentImagePath string

func TestMain(m *testing.M) {
	f := framework.NewFramework()
	err := f.SetUp()
	if err != nil {
		log.Fatal(err)
	}

	contentImagePath = os.Getenv("CONTENT_IMAGE")
	if contentImagePath == "" {
		fmt.Println("Please set the 'CONTENT_IMAGE' environment variable")
		os.Exit(1)
	}

	brokenContentImagePath = os.Getenv("BROKEN_CONTENT_IMAGE")

	if brokenContentImagePath == "" {
		fmt.Println("Please set the 'BROKEN_CONTENT_IMAGE' environment variable")
		os.Exit(1)
	}

	exitCode := m.Run()
	if exitCode == 0 || (exitCode > 0 && f.CleanUpOnError()) {
		if err = f.TearDown(); err != nil {
			log.Fatal(err)
		}
	}
	os.Exit(exitCode)
}

func TestProfileModification(t *testing.T) {
	t.Parallel()
	f := framework.Global
	const (
		removedRule         = "chronyd-no-chronyc-network"
		unlinkedRule        = "chronyd-client-only"
		moderateProfileName = "moderate"
	)
	var (
		baselineImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "proff_diff_baseline")
		modifiedImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "proff_diff_mod")
	)

	prefixName := func(profName, ruleBaseName string) string { return profName + "-" + ruleBaseName }

	pbName := framework.GetObjNameFromTest(t)
	origPb := &compv1alpha1.ProfileBundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pbName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ProfileBundleSpec{
			ContentImage: baselineImage,
			ContentFile:  framework.RhcosContentFile,
		},
	}
	// Pass nil in as the cleanupOptions since so we don't invoke all the
	// cleanup function code in Create. Use defer to cleanup the
	// ProfileBundle at the end of the test, instead of at the end of the
	// suite.
	if err := f.Client.Create(context.TODO(), origPb, nil); err != nil {
		t.Fatalf("failed to create ProfileBundle: %s", err)
	}
	// This should get cleaned up at the end of the test
	defer f.Client.Delete(context.TODO(), origPb)

	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatalf("failed waiting for the ProfileBundle to become available: %s", err)
	}
	if err := f.AssertMustHaveParsedProfiles(pbName, string(compv1alpha1.ScanTypeNode), "redhat_enterprise_linux_coreos_4"); err != nil {
		t.Fatalf("failed checking profiles in ProfileBundle: %s", err)
	}

	// Check that the rule we removed exists in the original profile
	removedRuleName := prefixName(pbName, removedRule)
	err, found := f.DoesRuleExist(origPb.Namespace, removedRuleName)
	if err != nil {
		t.Fatal(err)
	} else if found != true {
		t.Fatalf("expected rule %s to exist in namespace %s", removedRuleName, origPb.Namespace)
	}

	// Check that the rule we unlined in the modified profile is linked in the original
	profileName := prefixName(pbName, moderateProfileName)
	profilePreUpdate := &compv1alpha1.Profile{}
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: origPb.Namespace, Name: profileName}, profilePreUpdate); err != nil {
		t.Fatalf("failed to get profile %s", profileName)
	}
	unlinkedRuleName := prefixName(pbName, unlinkedRule)
	found = framework.IsRuleInProfile(unlinkedRuleName, profilePreUpdate)
	if found == false {
		t.Fatalf("failed to find rule %s in profile %s", unlinkedRule, profileName)
	}

	// update the image with a new hash
	modPb := origPb.DeepCopy()
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: modPb.Namespace, Name: modPb.Name}, modPb); err != nil {
		t.Fatalf("failed to get ProfileBundle %s", modPb.Name)
	}

	modPb.Spec.ContentImage = modifiedImage
	if err := f.Client.Update(context.TODO(), modPb); err != nil {
		t.Fatalf("failed to update ProfileBundle %s: %s", modPb.Name, err)
	}

	// Wait for the update to happen, the PB will flip first to pending, then to valid
	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatalf("failed to parse ProfileBundle %s: %s", pbName, err)
	}

	if err := f.AssertProfileBundleMustHaveParsedRules(pbName); err != nil {
		t.Fatal(err)
	}

	// We removed this rule in the update, is must no longer exist
	err, found = f.DoesRuleExist(origPb.Namespace, removedRuleName)
	if err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatalf("rule %s unexpectedly found", removedRuleName)
	}

	// This rule was unlinked
	profilePostUpdate := &compv1alpha1.Profile{}
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: origPb.Namespace, Name: profileName}, profilePostUpdate); err != nil {
		t.Fatalf("failed to get profile %s: %s", profileName, err)
	}
	framework.IsRuleInProfile(unlinkedRuleName, profilePostUpdate)
	if found {
		t.Fatalf("rule %s unexpectedly found", unlinkedRuleName)
	}
}

func TestProfileISTagUpdate(t *testing.T) {
	t.Parallel()
	f := framework.Global
	const (
		removedRule         = "chronyd-no-chronyc-network"
		unlinkedRule        = "chronyd-client-only"
		moderateProfileName = "moderate"
	)
	var (
		baselineImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "proff_diff_baseline")
		modifiedImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "proff_diff_mod")
	)

	prefixName := func(profName, ruleBaseName string) string { return profName + "-" + ruleBaseName }

	pbName := framework.GetObjNameFromTest(t)
	iSName := pbName

	s, err := f.CreateImageStream(iSName, f.OperatorNamespace, baselineImage)
	if err != nil {
		t.Fatalf("failed to create image stream %s", iSName)
	}
	defer f.Client.Delete(context.TODO(), s)

	pb := &compv1alpha1.ProfileBundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pbName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ProfileBundleSpec{
			ContentImage: fmt.Sprintf("%s:%s", iSName, "latest"),
			ContentFile:  framework.RhcosContentFile,
		},
	}

	if err := f.Client.Create(context.TODO(), pb, nil); err != nil {
		t.Fatalf("failed to create ProfileBundle %s", pbName)
	}
	defer f.Client.Delete(context.TODO(), pb)

	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatalf("failed waiting for the ProfileBundle to become available: %s", err)
	}
	if err := f.AssertMustHaveParsedProfiles(pbName, string(compv1alpha1.ScanTypeNode), "redhat_enterprise_linux_coreos_4"); err != nil {
		t.Fatalf("failed checking profiles in ProfileBundle: %s", err)
	}

	// Check that the rule we removed exists in the original profile
	removedRuleName := prefixName(pbName, removedRule)
	err, found := f.DoesRuleExist(pb.Namespace, removedRuleName)
	if err != nil {
		t.Fatal(err)
	} else if !found {
		t.Fatalf("failed to find rule %s in ProfileBundle %s", removedRuleName, pbName)
	}

	// Check that the rule we unlined in the modified profile is linked in the original
	profilePreUpdate := &compv1alpha1.Profile{}
	profileName := prefixName(pbName, moderateProfileName)
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: pb.Namespace, Name: profileName}, profilePreUpdate); err != nil {
		t.Fatalf("failed to get profile %s", profileName)
	}
	unlinkedRuleName := prefixName(pbName, unlinkedRule)
	found = framework.IsRuleInProfile(unlinkedRuleName, profilePreUpdate)
	if !found {
		t.Fatalf("failed to find rule %s in ProfileBundle %s", unlinkedRuleName, pbName)
	}

	// Update the reference in the image stream
	if err := f.UpdateImageStreamTag(iSName, modifiedImage, f.OperatorNamespace); err != nil {
		t.Fatalf("failed to update image stream %s: %s", iSName, err)
	}

	modifiedImageDigest, err := f.GetImageStreamUpdatedDigest(iSName, f.OperatorNamespace)
	if err != nil {
		t.Fatalf("failed to get digest for image stream %s: %s", iSName, err)
	}

	// Note that when an update happens through an imagestream tag, the operator doesn't get
	// a notification about it... It all happens on the Kube Deployment's side.
	// So we don't need to wait for the profile bundle's statuses
	if err := f.WaitForDeploymentContentUpdate(pbName, modifiedImageDigest); err != nil {
		t.Fatalf("failed waiting for content to update: %s", err)
	}

	if err := f.AssertProfileBundleMustHaveParsedRules(pbName); err != nil {
		t.Fatal(err)
	}

	// We removed this rule in the update, it must no longer exist
	err, found = f.DoesRuleExist(pb.Namespace, removedRuleName)
	if err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatalf("rule %s unexpectedly found", removedRuleName)
	}

	// This rule was unlinked
	profilePostUpdate := &compv1alpha1.Profile{}
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: pb.Namespace, Name: profileName}, profilePostUpdate); err != nil {
		t.Fatalf("failed to get profile %s", profileName)
	}
	found = framework.IsRuleInProfile(unlinkedRuleName, profilePostUpdate)
	if found {
		t.Fatalf("rule %s unexpectedly found", unlinkedRuleName)
	}
}

func TestProfileISTagOtherNs(t *testing.T) {
	t.Parallel()
	f := framework.Global
	const (
		removedRule         = "chronyd-no-chronyc-network"
		unlinkedRule        = "chronyd-client-only"
		moderateProfileName = "moderate"
	)
	var (
		baselineImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "proff_diff_baseline")
		modifiedImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "proff_diff_mod")
	)

	prefixName := func(profName, ruleBaseName string) string { return profName + "-" + ruleBaseName }

	pbName := framework.GetObjNameFromTest(t)
	iSName := pbName
	otherNs := "openshift"

	stream, err := f.CreateImageStream(iSName, otherNs, baselineImage)
	if err != nil {
		t.Fatalf("failed to create image stream %s\n", iSName)
	}
	defer f.Client.Delete(context.TODO(), stream)

	pb := &compv1alpha1.ProfileBundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pbName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ProfileBundleSpec{
			ContentImage: fmt.Sprintf("%s/%s:%s", otherNs, iSName, "latest"),
			ContentFile:  framework.RhcosContentFile,
		},
	}

	if err := f.Client.Create(context.TODO(), pb, nil); err != nil {
		t.Fatalf("failed to create ProfileBundle %s: %s", pbName, err)
	}
	defer f.Client.Delete(context.TODO(), pb)

	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatalf("failed waiting for ProfileBundle to parse: %s", err)
	}
	if err := f.AssertMustHaveParsedProfiles(pbName, string(compv1alpha1.ScanTypeNode), "redhat_enterprise_linux_coreos_4"); err != nil {
		t.Fatalf("failed to assert profiles in ProfileBundle %s: %s", pbName, err)
	}

	// Check that the rule we removed exists in the original profile
	removedRuleName := prefixName(pbName, removedRule)
	err, found := f.DoesRuleExist(pb.Namespace, removedRuleName)
	if err != nil {
		t.Fatal(err)
	} else if !found {
		t.Fatalf("expected rule %s to exist", removedRuleName)
	}

	// Check that the rule we unlined in the modified profile is linked in the original
	profilePreUpdate := &compv1alpha1.Profile{}
	profileName := prefixName(pbName, moderateProfileName)
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: pb.Namespace, Name: profileName}, profilePreUpdate); err != nil {
		t.Fatalf("failed to get profile %s: %s", profileName, err)
	}
	unlinkedRuleName := prefixName(pbName, unlinkedRule)
	found = framework.IsRuleInProfile(unlinkedRuleName, profilePreUpdate)
	if !found {
		t.Fatalf("expected to find rule %s in profile %s", unlinkedRuleName, profileName)
	}

	// Update the reference in the image stream
	if err := f.UpdateImageStreamTag(iSName, modifiedImage, otherNs); err != nil {
		t.Fatalf("failed to update image stream %s: %s", iSName, err)
	}

	modifiedImageDigest, err := f.GetImageStreamUpdatedDigest(iSName, otherNs)
	if err != nil {
		t.Fatalf("failed to get digest for image stream %s: %s", iSName, err)
	}

	// Note that when an update happens through an imagestream tag, the operator doesn't get
	// a notification about it... It all happens on the Kube Deployment's side.
	// So we don't need to wait for the profile bundle's statuses
	if err := f.WaitForDeploymentContentUpdate(pbName, modifiedImageDigest); err != nil {
		t.Fatalf("failed waiting for content to update: %s", err)
	}

	if err := f.AssertProfileBundleMustHaveParsedRules(pbName); err != nil {
		t.Fatal(err)
	}
	// We removed this rule in the update, it must no longer exist
	err, found = f.DoesRuleExist(pb.Namespace, removedRuleName)
	if err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatalf("rule %s unexpectedly found", removedRuleName)
	}

	// This rule was unlinked
	profilePostUpdate := &compv1alpha1.Profile{}
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: pb.Namespace, Name: profileName}, profilePostUpdate); err != nil {
		t.Fatalf("failed to get profile %s", profileName)
	}
	found = framework.IsRuleInProfile(unlinkedRuleName, profilePostUpdate)
	if found {
		t.Fatalf("rule %s unexpectedly found", unlinkedRuleName)
	}

}

func TestInvalidBundleWithUnexistentRef(t *testing.T) {
	t.Parallel()
	f := framework.Global
	const (
		unexistentImage = "bad-namespace/bad-image:latest"
	)

	pbName := framework.GetObjNameFromTest(t)

	pb := &compv1alpha1.ProfileBundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pbName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ProfileBundleSpec{
			ContentImage: unexistentImage,
			ContentFile:  framework.RhcosContentFile,
		},
	}

	if err := f.Client.Create(context.TODO(), pb, nil); err != nil {
		t.Fatalf("failed to create ProfileBundle %s: %s", pbName, err)
	}
	defer f.Client.Delete(context.TODO(), pb)

	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamInvalid); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidBundleWithNoTag(t *testing.T) {
	t.Parallel()
	f := framework.Global
	const (
		noTagImage = "bad-namespace/bad-image"
	)

	pbName := framework.GetObjNameFromTest(t)

	pb := &compv1alpha1.ProfileBundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pbName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ProfileBundleSpec{
			ContentImage: noTagImage,
			ContentFile:  framework.RhcosContentFile,
		},
	}

	if err := f.Client.Create(context.TODO(), pb, nil); err != nil {
		t.Fatalf("failed to create ProfileBundle %s: %s", pbName, err)
	}
	defer f.Client.Delete(context.TODO(), pb)

	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamInvalid); err != nil {
		t.Fatal(err)
	}
}

func TestParsingErrorRestartsParserInitContainer(t *testing.T) {
	t.Parallel()
	f := framework.Global
	var (
		badImage  = fmt.Sprintf("%s:%s", brokenContentImagePath, "from")
		goodImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "to")
	)

	pbName := framework.GetObjNameFromTest(t)

	pb := &compv1alpha1.ProfileBundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pbName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ProfileBundleSpec{
			ContentImage: badImage,
			ContentFile:  framework.OcpContentFile,
		},
	}

	if err := f.Client.Create(context.TODO(), pb, nil); err != nil {
		t.Fatalf("failed to create ProfileBundle %s: %s", pbName, err)
	}
	defer f.Client.Delete(context.TODO(), pb)

	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamInvalid); err != nil {
		t.Fatal(err)
	}

	// list the pods with profilebundle=pbName
	var lastErr error
	timeouterr := wait.Poll(framework.RetryInterval, framework.Timeout, func() (bool, error) {
		podList := &corev1.PodList{}
		inNs := client.InNamespace(f.OperatorNamespace)
		withLabel := client.MatchingLabels{"profile-bundle": pbName}
		if lastErr := f.Client.List(context.TODO(), podList, inNs, withLabel); lastErr != nil {
			return false, lastErr
		}

		if len(podList.Items) != 1 {
			return false, fmt.Errorf("expected one parser pod, listed %d", len(podList.Items))
		}
		parserPod := &podList.Items[0]

		// check that pod's initContainerStatuses field with name=profileparser has restartCount > 0 and that
		// lastState.Terminated.ExitCode != 0. This way we'll know we're restarting the init container
		// and retrying the parsing
		for i := range parserPod.Status.InitContainerStatuses {
			ics := parserPod.Status.InitContainerStatuses[i]
			if ics.Name != "profileparser" {
				continue
			}
			if ics.RestartCount < 1 {
				log.Println("The profileparser did not restart (yet?)")
				return false, nil
			}

			// wait until we get the restarted state
			if ics.LastTerminationState.Terminated == nil {
				log.Println("The profileparser does not have terminating state")
				return false, nil
			}
			if ics.LastTerminationState.Terminated.ExitCode == 0 {
				return true, fmt.Errorf("profileparser finished unsuccessfully")
			}
		}

		return true, nil
	})

	if err := framework.ProcessErrorOrTimeout(lastErr, timeouterr, "waiting for ProfileBundle parser to restart"); err != nil {
		t.Fatal(err)
	}

	// Fix the image and wait for the profilebundle to be parsed OK
	getPb := &compv1alpha1.ProfileBundle{}
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Name: pbName, Namespace: f.OperatorNamespace}, getPb); err != nil {
		t.Fatalf("failed to get ProfileBundle %s: %s", pbName, err)
	}

	updatePb := getPb.DeepCopy()
	updatePb.Spec.ContentImage = goodImage
	if err := f.Client.Update(context.TODO(), updatePb); err != nil {
		t.Fatalf("failed to update ProfileBundle %s: %s", pbName, err)
	}

	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatal(err)
	}
	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatal(err)
	}
}

func TestRulesAreClassifiedAppropriately(t *testing.T) {
	t.Parallel()
	f := framework.Global
	for _, expected := range []struct {
		RuleName  string
		CheckType string
	}{
		{
			"ocp4-configure-network-policies-namespaces",
			compv1alpha1.CheckTypePlatform,
		},
		{
			"ocp4-directory-access-var-log-kube-audit",
			compv1alpha1.CheckTypeNode,
		},
		{
			"ocp4-general-apply-scc",
			compv1alpha1.CheckTypeNone,
		},
		{
			"ocp4-kubelet-enable-protect-kernel-sysctl",
			compv1alpha1.CheckTypeNode,
		},
	} {
		targetRule := &compv1alpha1.Rule{}
		key := types.NamespacedName{
			Name:      expected.RuleName,
			Namespace: f.OperatorNamespace,
		}

		if err := f.Client.Get(context.TODO(), key, targetRule); err != nil {
			t.Fatalf("failed to get rule %s: %s", targetRule.Name, err)
		}

		if targetRule.CheckType != expected.CheckType {
			log.Printf("Expected rule '%s' to be of type '%s'. Instead was: '%s'",
				expected.RuleName, expected.CheckType, targetRule.CheckType)
		}
	}
}

func TestSingleScanSucceeds(t *testing.T) {
	t.Parallel()
	f := framework.Global

	scanName := framework.GetObjNameFromTest(t)
	testScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile: "xccdf_org.ssgproject.content_profile_moderate",
			Content: framework.RhcosContentFile,
			Rule:    "xccdf_org.ssgproject.content_rule_no_netrc_files",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testScan, nil)
	if err != nil {
		t.Fatalf("failed to create scan %s: %s", scanName, err)
	}
	defer f.Client.Delete(context.TODO(), testScan)

	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	err = f.AssertScanIsCompliant(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}

	aggrString := fmt.Sprintf("compliance_operator_compliance_scan_status_total{name=\"%s\",phase=\"AGGREGATING\",result=\"NOT-AVAILABLE\"}", scanName)
	metricsSet := map[string]int{
		fmt.Sprintf("compliance_operator_compliance_scan_status_total{name=\"%s\",phase=\"DONE\",result=\"COMPLIANT\"}", scanName):          1,
		fmt.Sprintf("compliance_operator_compliance_scan_status_total{name=\"%s\",phase=\"LAUNCHING\",result=\"NOT-AVAILABLE\"}", scanName): 1,
		fmt.Sprintf("compliance_operator_compliance_scan_status_total{name=\"%s\",phase=\"PENDING\",result=\"\"}", scanName):                1,
		fmt.Sprintf("compliance_operator_compliance_scan_status_total{name=\"%s\",phase=\"RUNNING\",result=\"NOT-AVAILABLE\"}", scanName):   1,
	}

	var metErr error
	// Aggregating may be variable, could be registered 1 to 3 times.
	for i := 1; i < 4; i++ {
		metricsSet[aggrString] = i
		err = framework.AssertEachMetric(f.OperatorNamespace, metricsSet)
		if err == nil {
			metErr = nil
			break
		}
		metErr = err
	}

	if metErr != nil {
		t.Fatalf("failed to assert metrics for scan %s: %s\n", scanName, metErr)
	}

	err = f.AssertScanHasValidPVCReference(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatalf("failed to assert PVC reference for scan %s: %s", scanName, err)
	}
}

func TestScanProducesRemediations(t *testing.T) {
	t.Parallel()
	f := framework.Global
	bindingName := framework.GetObjNameFromTest(t)
	tpName := framework.GetObjNameFromTest(t)

	// When using a profile directly, the profile name gets re-used
	// in the scan. By using a tailored profile we ensure that
	// the scan is unique and we get no clashes.
	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "TestScanProducesRemediations",
			Description: "TestScanProducesRemediations",
			Extends:     "ocp4-moderate",
		},
	}

	createTPErr := f.Client.Create(context.TODO(), tp, nil)
	if createTPErr != nil {
		t.Fatal(createTPErr)
	}
	defer f.Client.Delete(context.TODO(), tp)
	scanSettingBinding := compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bindingName,
			Namespace: f.OperatorNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				Name:     tpName,
				Kind:     "TailoredProfile",
				APIGroup: "compliance.openshift.io/v1alpha1",
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			Name:     "default",
			Kind:     "ScanSetting",
			APIGroup: "compliance.openshift.io/v1alpha1",
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), &scanSettingBinding, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), &scanSettingBinding)
	if err := f.WaitForSuiteScansStatus(f.OperatorNamespace, bindingName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant); err != nil {
		t.Fatal(err)
	}

	// Since the scan was not compliant, there should be some remediations and none
	// of them should be an error
	inNs := client.InNamespace(f.OperatorNamespace)
	withLabel := client.MatchingLabels{compv1alpha1.SuiteLabel: bindingName}
	fmt.Println(inNs, withLabel)
	remList := &compv1alpha1.ComplianceRemediationList{}
	err = f.Client.List(context.TODO(), remList, inNs, withLabel)
	if err != nil {
		t.Fatal(err)
	}

	if len(remList.Items) == 0 {
		t.Fatal("expected at least one remediation")
	}
	for _, rem := range remList.Items {
		if rem.Status.ApplicationState != compv1alpha1.RemediationNotApplied {
			t.Fatal("expected all remediations are unapplied when scan finishes")
		}
	}
}

func TestSingleScanWithStorageSucceeds(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := framework.GetObjNameFromTest(t)
	testScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile: "xccdf_org.ssgproject.content_profile_moderate",
			Content: framework.RhcosContentFile,
			Rule:    "xccdf_org.ssgproject.content_rule_no_netrc_files",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				RawResultStorage: compv1alpha1.RawResultStorageSettings{
					Size: "2Gi",
				},
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	err = f.AssertScanIsCompliant(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanHasValidPVCReferenceWithSize(scanName, "2Gi", f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanWithUnexistentResourceFails(t *testing.T) {
	t.Parallel()
	f := framework.Global
	var unexistentImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "unexistent_resource")
	scanName := framework.GetObjNameFromTest(t)
	testScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_test",
			Content:      framework.UnexistentResourceContentFile,
			ContentImage: unexistentImage,
			Rule:         "xccdf_org.ssgproject.content_rule_api_server_unexistent_resource",
			ScanType:     compv1alpha1.ScanTypePlatform,
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	err = f.AssertScanIsNonCompliant(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}

	if err = f.ScanHasWarnings(scanName, f.OperatorNamespace); err != nil {
		t.Fatal(err)
	}
}

func TestScanStorageOutOfLimitRangeFails(t *testing.T) {
	t.Parallel()
	f := framework.Global
	// Create LimitRange
	lr := &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pvc-limitrange",
			Namespace: f.OperatorNamespace,
		},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{
				{
					Type: corev1.LimitTypePersistentVolumeClaim,
					Max: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("5Gi"),
					},
				},
			},
		},
	}
	if err := f.Client.Create(context.TODO(), lr, nil); err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), lr)

	scanName := framework.GetObjNameFromTest(t)
	testScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile: "xccdf_org.ssgproject.content_profile_moderate",
			Content: framework.RhcosContentFile,
			Rule:    "xccdf_org.ssgproject.content_rule_no_netrc_files",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				RawResultStorage: compv1alpha1.RawResultStorageSettings{
					Size: "6Gi",
				},
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testScan)
	f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	err = f.AssertScanIsInError(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}

}

func TestSingleTailoredScanSucceeds(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := framework.GetObjNameFromTest(t)
	tailoringCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-single-tailored-scan-succeeds-cm",
			Namespace: f.OperatorNamespace,
		},
		Data: map[string]string{
			"tailoring.xml": `<?xml version="1.0" encoding="UTF-8"?>
<xccdf-1.2:Tailoring xmlns:xccdf-1.2="http://checklists.nist.gov/xccdf/1.2" id="xccdf_compliance.openshift.io_tailoring_test-tailoredprofile">
<xccdf-1.2:benchmark href="/content/ssg-rhcos4-ds.xml"></xccdf-1.2:benchmark>
<xccdf-1.2:version time="2020-04-28T07:04:13Z">1</xccdf-1.2:version>
<xccdf-1.2:Profile id="xccdf_compliance.openshift.io_profile_test-tailoredprofile">
<xccdf-1.2:title>Test Tailored Profile</xccdf-1.2:title>
<xccdf-1.2:description>Test Tailored Profile</xccdf-1.2:description>
<xccdf-1.2:select idref="xccdf_org.ssgproject.content_rule_no_netrc_files" selected="true"></xccdf-1.2:select>
</xccdf-1.2:Profile>
</xccdf-1.2:Tailoring>`,
		},
	}

	err := f.Client.Create(context.TODO(), tailoringCM, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), tailoringCM)

	exampleComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile: "xccdf_compliance.openshift.io_profile_test-tailoredprofile",
			Content: framework.RhcosContentFile,
			Rule:    "xccdf_org.ssgproject.content_rule_no_netrc_files",
			TailoringConfigMap: &compv1alpha1.TailoringConfigMapRef{
				Name: tailoringCM.Name,
			},
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err = f.Client.Create(context.TODO(), exampleComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), exampleComplianceScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanIsCompliant(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSingleTailoredPlatformScanSucceeds(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := framework.GetObjNameFromTest(t)
	tailoringCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tailored-platform-scan-succeeds-cm",
			Namespace: f.OperatorNamespace,
		},
		Data: map[string]string{
			"tailoring.xml": `<?xml version="1.0" encoding="UTF-8"?>
<xccdf-1.2:Tailoring xmlns:xccdf-1.2="http://checklists.nist.gov/xccdf/1.2" id="xccdf_compliance.openshift.io_tailoring_tailoredplatformprofile">
<xccdf-1.2:benchmark href="/content/ssg-ocp4-ds.xml"></xccdf-1.2:benchmark>
<xccdf-1.2:version time="2020-11-27T11:58:27Z">1</xccdf-1.2:version>
<xccdf-1.2:Profile id="xccdf_compliance.openshift.io_profile_test-tailoredplatformprofile">
<xccdf-1.2:title override="true">Test Tailored Platform profile</xccdf-1.2:title>
<xccdf-1.2:description override="true">This is a test for platform profile tailoring</xccdf-1.2:description>
<xccdf-1.2:select idref="xccdf_org.ssgproject.content_rule_cluster_version_operator_exists" selected="true"></xccdf-1.2:select>
</xccdf-1.2:Profile>
</xccdf-1.2:Tailoring>`,
		},
	}

	err := f.Client.Create(context.TODO(), tailoringCM, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), tailoringCM)

	exampleComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			ScanType:     compv1alpha1.ScanTypePlatform,
			ContentImage: contentImagePath,
			Profile:      "xccdf_compliance.openshift.io_profile_test-tailoredplatformprofile",
			Rule:         "xccdf_org.ssgproject.content_rule_cluster_version_operator_exists",
			Content:      framework.OcpContentFile,
			TailoringConfigMap: &compv1alpha1.TailoringConfigMapRef{
				Name: tailoringCM.Name,
			},
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err = f.Client.Create(context.TODO(), exampleComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), exampleComplianceScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	err = f.AssertScanIsCompliant(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanWithNodeSelectorFiltersCorrectly(t *testing.T) {
	t.Parallel()
	f := framework.Global
	selectWorkers := map[string]string{
		"node-role.kubernetes.io/worker": "",
	}
	testComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-filtered-scan",
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_moderate",
			Content:      framework.RhcosContentFile,
			Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
			NodeSelector: selectWorkers,
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testComplianceScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, "test-filtered-scan", compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	nodes, err := f.GetNodesWithSelector(selectWorkers)
	if err != nil {
		t.Fatal(err)
	}
	configmaps, err := f.GetConfigMapsFromScan(testComplianceScan)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != len(configmaps) {
		t.Fatalf("The number of reports doesn't match the number of selected nodes: %d reports / %d nodes", len(configmaps), len(nodes))
	}
	err = f.AssertScanIsCompliant("test-filtered-scan", f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanWithNodeSelectorNoMatches(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := framework.GetObjNameFromTest(t)
	selectNone := map[string]string{
		"node-role.kubernetes.io/no-matches": "",
	}
	testComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_moderate",
			Content:      framework.RhcosContentFile,
			Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
			NodeSelector: selectNone,
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug:             true,
				ShowNotApplicable: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testComplianceScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanIsNotApplicable(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanWithInvalidScanTypeFails(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := framework.GetObjNameFromTest(t)
	testScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:  "xccdf_org.ssgproject.content_profile_moderate",
			Content:  "ssg-ocp4-non-existent.xml",
			ScanType: "BadScanType",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanIsInError(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanWithInvalidContentFails(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := "test-scan-w-invalid-content"
	exampleComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile: "xccdf_org.ssgproject.content_profile_moderate",
			Content: "ssg-ocp4-non-existent.xml",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), exampleComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), exampleComplianceScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanIsInError(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanWithInvalidProfileFails(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := "test-scan-w-invalid-profile"
	exampleComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile: "xccdf_org.ssgproject.content_profile_coreos-unexistent",
			Content: framework.RhcosContentFile,
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), exampleComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), exampleComplianceScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanIsInError(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMalformedTailoredScanFails(t *testing.T) {
	t.Parallel()
	f := framework.Global
	cmName := "test-malformed-tailored-scan-fails-cm"
	tailoringCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: f.OperatorNamespace,
		},
		// The tailored profile's namespace is wrong. It should be xccdf-1.2, but it was
		// declared as xccdf. So it should report an error
		Data: map[string]string{
			"tailoring.xml": `<?xml version="1.0" encoding="UTF-8"?>
<xccdf-1.2:Tailoring xmlns:xccdf="http://checklists.nist.gov/xccdf/1.2" id="xccdf_compliance.openshift.io_tailoring_test-tailoredprofile">
<xccdf-1.2:benchmark href="/content/ssg-rhcos4-ds.xml"></xccdf-1.2:benchmark>
<xccdf-1.2:version time="2020-04-28T07:04:13Z">1</xccdf-1.2:version>
<xccdf-1.2:Profile id="xccdf_compliance.openshift.io_profile_test-tailoredprofile">
<xccdf-1.2:title>Test Tailored Profile</xccdf-1.2:title>
<xccdf-1.2:description>Test Tailored Profile</xccdf-1.2:description>
<xccdf-1.2:select idref="xccdf_org.ssgproject.content_rule_no_netrc_files" selected="true"></xccdf-1.2:select>
</xccdf-1.2:Profile>
</xccdf-1.2:Tailoring>`,
		},
	}

	err := f.Client.Create(context.TODO(), tailoringCM, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), tailoringCM)

	scanName := "test-malformed-tailored-scan-fails"
	exampleComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile: "xccdf_compliance.openshift.io_profile_test-tailoredprofile",
			Content: framework.RhcosContentFile,
			Rule:    "xccdf_org.ssgproject.content_rule_no_netrc_files",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
			TailoringConfigMap: &compv1alpha1.TailoringConfigMapRef{
				Name: tailoringCM.Name,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err = f.Client.Create(context.TODO(), exampleComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), exampleComplianceScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanIsInError(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanWithEmptyTailoringCMNameFails(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := "test-scan-w-empty-tailoring-cm"
	exampleComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile: "xccdf_org.ssgproject.content_profile_moderate",
			Content: framework.RhcosContentFile,
			Rule:    "xccdf_org.ssgproject.content_rule_no_netrc_files",
			TailoringConfigMap: &compv1alpha1.TailoringConfigMapRef{
				Name: "",
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), exampleComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), exampleComplianceScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	err = f.AssertScanIsInError(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanWithMissingTailoringCMFailsAndRecovers(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := "test-scan-w-missing-tailoring-cm"
	exampleComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile: "xccdf_compliance.openshift.io_profile_test-tailoredprofile",
			Content: framework.RhcosContentFile,
			Rule:    "xccdf_org.ssgproject.content_rule_no_netrc_files",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
			TailoringConfigMap: &compv1alpha1.TailoringConfigMapRef{
				Name: "missing-tailoring-file",
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), exampleComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), exampleComplianceScan)

	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseLaunching)
	if err != nil {
		t.Fatal(err)
	}

	var resultErr error
	// The status might still be NOT-AVAILABLE... we can wait a bit
	// for the reconciliation to update it.
	_ = wait.PollImmediate(framework.RetryInterval, framework.Timeout, func() (bool, error) {
		if resultErr = f.AssertScanIsInError(scanName, f.OperatorNamespace); resultErr != nil {
			return false, nil
		}
		return true, nil
	})
	if resultErr != nil {
		t.Fatalf("failed waiting for the config map: %s", resultErr)
	}

	tailoringCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "missing-tailoring-file",
			Namespace: f.OperatorNamespace,
		},
		Data: map[string]string{
			"tailoring.xml": `<?xml version="1.0" encoding="UTF-8"?>
<xccdf-1.2:Tailoring xmlns:xccdf-1.2="http://checklists.nist.gov/xccdf/1.2" id="xccdf_compliance.openshift.io_tailoring_test-tailoredprofile">
<xccdf-1.2:benchmark href="/content/ssg-rhcos4-ds.xml"></xccdf-1.2:benchmark>
<xccdf-1.2:version time="2020-04-28T07:04:13Z">1</xccdf-1.2:version>
<xccdf-1.2:Profile id="xccdf_compliance.openshift.io_profile_test-tailoredprofile">
<xccdf-1.2:title>Test Tailored Profile</xccdf-1.2:title>
<xccdf-1.2:description>Test Tailored Profile</xccdf-1.2:description>
<xccdf-1.2:select idref="xccdf_org.ssgproject.content_rule_no_netrc_files" selected="true"></xccdf-1.2:select>
</xccdf-1.2:Profile>
</xccdf-1.2:Tailoring>`,
		},
	}
	err = f.Client.Create(context.TODO(), tailoringCM, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), tailoringCM)

	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanIsCompliant(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMissingPodInRunningState(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := "test-missing-pod-scan"
	exampleComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile: "xccdf_org.ssgproject.content_profile_moderate",
			Content: framework.RhcosContentFile,
			Rule:    "xccdf_org.ssgproject.content_rule_no_netrc_files",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), exampleComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), exampleComplianceScan)

	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseRunning)
	if err != nil {
		t.Fatal(err)
	}
	pods, err := f.GetPodsForScan(scanName)
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) < 1 {
		t.Fatal("No pods gotten from query for the scan")
	}
	podToDelete := pods[rand.Intn(len(pods))]
	// Delete pod ASAP
	zeroSeconds := int64(0)
	do := client.DeleteOptions{GracePeriodSeconds: &zeroSeconds}
	err = f.Client.Delete(context.TODO(), &podToDelete, &do)
	if err != nil {
		t.Fatal(err)
	}
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	err = f.AssertScanIsCompliant(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestApplyGenericRemediation(t *testing.T) {
	t.Parallel()
	f := framework.Global
	remName := "test-apply-generic-remediation"
	unstruct := &unstructured.Unstructured{}
	unstruct.SetUnstructuredContent(map[string]interface{}{
		"kind":       "ConfigMap",
		"apiVersion": "v1",
		"metadata": map[string]interface{}{
			"name":      "generic-rem-cm",
			"namespace": f.OperatorNamespace,
		},
		"data": map[string]interface{}{
			"key": "value",
		},
	})

	genericRem := &compv1alpha1.ComplianceRemediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      remName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceRemediationSpec{
			ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
				Apply: true,
			},
			Current: compv1alpha1.ComplianceRemediationPayload{
				Object: unstruct,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), genericRem, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), genericRem)
	err = f.WaitForRemediationState(remName, f.OperatorNamespace, compv1alpha1.RemediationApplied)
	if err != nil {
		t.Fatal(err)
	}

	cm := &corev1.ConfigMap{}
	cmName := "generic-rem-cm"
	err = f.WaitForObjectToExist(cmName, f.OperatorNamespace, cm)
	if err != nil {
		t.Fatal(err)
	}
	val, ok := cm.Data["key"]
	if !ok || val != "value" {
		t.Fatalf("ComplianceRemediation '%s' generated a malformed ConfigMap", remName)
	}

	// verify object is marked as created by the operator
	if !compv1alpha1.RemediationWasCreatedByOperator(cm) {
		t.Fatalf("ComplianceRemediation '%s' is missing controller annotation '%s'",
			remName, compv1alpha1.RemediationCreatedByOperatorAnnotation)
	}
}

func TestPatchGenericRemediation(t *testing.T) {
	t.Parallel()
	f := framework.Global
	remName := framework.GetObjNameFromTest(t)
	cmName := remName
	cmKey := types.NamespacedName{
		Name:      cmName,
		Namespace: f.OperatorNamespace,
	}
	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmKey.Name,
			Namespace: cmKey.Namespace,
		},
		Data: map[string]string{
			"existingKey": "existingData",
		},
	}

	if err := f.Client.Create(context.TODO(), existingCM, nil); err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), existingCM)

	cm := &corev1.ConfigMap{}
	err := f.WaitForObjectToExist(cmKey.Name, f.OperatorNamespace, cm)
	if err != nil {
		t.Fatal(err)
	}

	unstruct := &unstructured.Unstructured{}
	unstruct.SetUnstructuredContent(map[string]interface{}{
		"kind":       "ConfigMap",
		"apiVersion": "v1",
		"metadata": map[string]interface{}{
			"name":      cmKey.Name,
			"namespace": cmKey.Namespace,
		},
		"data": map[string]interface{}{
			"newKey": "newData",
		},
	})

	genericRem := &compv1alpha1.ComplianceRemediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      remName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceRemediationSpec{
			ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
				Apply: true,
			},
			Current: compv1alpha1.ComplianceRemediationPayload{
				Object: unstruct,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err = f.Client.Create(context.TODO(), genericRem, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), genericRem)

	err = f.WaitForRemediationState(remName, f.OperatorNamespace, compv1alpha1.RemediationApplied)
	if err != nil {
		t.Fatal(err)
	}

	err = f.WaitForObjectToUpdate(cmKey.Name, f.OperatorNamespace, cm)
	if err != nil {
		t.Fatal(err)
	}

	// Old data should still be there
	val, ok := cm.Data["existingKey"]
	if !ok || val != "existingData" {
		t.Fatalf("ComplianceRemediation '%s' generated a malformed ConfigMap", remName)
	}

	// new data should be there too
	val, ok = cm.Data["newKey"]
	if !ok || val != "newData" {
		t.Fatalf("ComplianceRemediation '%s' generated a malformed ConfigMap", remName)
	}
}

func TestGenericRemediationFailsWithUnkownType(t *testing.T) {
	t.Parallel()
	f := framework.Global
	remName := "test-generic-remediation-fails-unkown"
	genericRem := &compv1alpha1.ComplianceRemediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      remName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceRemediationSpec{
			ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
				Apply: true,
			},
			Current: compv1alpha1.ComplianceRemediationPayload{
				Object: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"kind":       "OopsyDoodle",
						"apiVersion": "foo.bar/v1",
						"metadata": map[string]interface{}{
							"name":      "unkown-remediation",
							"namespace": f.OperatorNamespace,
						},
						"data": map[string]interface{}{
							"key": "value",
						},
					},
				},
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), genericRem, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), genericRem)
	err = f.WaitForRemediationState(remName, f.OperatorNamespace, compv1alpha1.RemediationError)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSuiteWithInvalidScheduleShowsError(t *testing.T) {
	t.Parallel()
	f := framework.Global
	suiteName := "test-suite-with-invalid-schedule"
	testSuite := &compv1alpha1.ComplianceSuite{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceSuiteSpec{
			ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
				AutoApplyRemediations: false,
				Schedule:              "This is WRONG",
			},
			Scans: []compv1alpha1.ComplianceScanSpecWrapper{
				{
					Name: fmt.Sprintf("%s-workers-scan", suiteName),
					ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
						ContentImage: contentImagePath,
						Profile:      "xccdf_org.ssgproject.content_profile_moderate",
						Content:      framework.RhcosContentFile,
						ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
							Debug: true,
						},
						NodeSelector: map[string]string{
							"node-role.kubernetes.io/worker": "",
						},
					},
				},
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testSuite, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testSuite)

	err = f.WaitForSuiteScansStatus(f.OperatorNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultError)
	if err != nil {
		t.Fatal(err)
	}
	err = f.SuiteErrorMessageMatchesRegex(f.OperatorNamespace, suiteName, "Suite was invalid: .*")
	if err != nil {
		t.Fatal(err)
	}
}

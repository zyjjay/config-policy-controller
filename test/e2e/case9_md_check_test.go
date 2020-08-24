// Copyright (c) 2020 Red Hat, Inc.

package e2e

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/open-cluster-management/config-policy-controller/test/utils"
)

const case9ConfigPolicyNamePod string = "policy-pod-c9-create"
const case9ConfigPolicyNameAnno string = "policy-pod-anno"
const case9ConfigPolicyNameNoAnno string = "policy-pod-no-anno"
const case9ConfigPolicyNameLabelPatch string = "policy-label-patch"
const case9ConfigPolicyNameLabelCheck string = "policy-label-check"
const case9PolicyYamlPod string = "../resources/case9_md_check/case9_pod_create.yaml"
const case9PolicyYamlAnno string = "../resources/case9_md_check/case9_annos.yaml"
const case9PolicyYamlNoAnno string = "../resources/case9_md_check/case9_no_annos.yaml"
const case9PolicyYamlLabelPatch string = "../resources/case9_md_check/case9_label_patch.yaml"
const case9PolicyYamlLabelCheck string = "../resources/case9_md_check/case9_label_check.yaml"

var _ = Describe("Test pod obj template handling", func() {
	Describe("Create a pod policy on managed cluster in ns:"+testNamespace, func() {
		It("should create a policy properly on the managed cluster", func() {
			By("Creating " + case9ConfigPolicyNamePod + " on managed")
			utils.Kubectl("apply", "-f", case9PolicyYamlPod, "-n", testNamespace)
			plc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigPolicy, case9ConfigPolicyNamePod, testNamespace, true, defaultTimeoutSeconds)
			Expect(plc).NotTo(BeNil())
			Eventually(func() interface{} {
				managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigPolicy, case9ConfigPolicyNamePod, testNamespace, true, defaultTimeoutSeconds)
				return utils.GetComplianceState(managedPlc)
			}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		})
		It("should check annotations of the created policy", func() {
			By("Creating " + case9ConfigPolicyNameAnno + " on managed")
			utils.Kubectl("apply", "-f", case9PolicyYamlAnno, "-n", testNamespace)
			plc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigPolicy, case9ConfigPolicyNameAnno, testNamespace, true, defaultTimeoutSeconds)
			Expect(plc).NotTo(BeNil())
			Eventually(func() interface{} {
				managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigPolicy, case9ConfigPolicyNameAnno, testNamespace, true, defaultTimeoutSeconds)
				return utils.GetComplianceState(managedPlc)
			}, defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
		})
		It("should return compliant if lack of annotations matches", func() {
			By("Creating " + case9ConfigPolicyNameNoAnno + " on managed")
			utils.Kubectl("apply", "-f", case9PolicyYamlNoAnno, "-n", testNamespace)
			plc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigPolicy, case9ConfigPolicyNameNoAnno, testNamespace, true, defaultTimeoutSeconds)
			Expect(plc).NotTo(BeNil())
			Eventually(func() interface{} {
				managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigPolicy, case9ConfigPolicyNameNoAnno, testNamespace, true, defaultTimeoutSeconds)
				return utils.GetComplianceState(managedPlc)
			}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		})
		It("should patch labels/annotations properly if enforce", func() {
			By("Creating " + case9ConfigPolicyNameLabelPatch + " on managed")
			utils.Kubectl("apply", "-f", case9PolicyYamlLabelPatch, "-n", testNamespace)
			plc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigPolicy, case9ConfigPolicyNameLabelPatch, testNamespace, true, defaultTimeoutSeconds)
			Expect(plc).NotTo(BeNil())
			Eventually(func() interface{} {
				managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigPolicy, case9ConfigPolicyNameLabelPatch, testNamespace, true, defaultTimeoutSeconds)
				return utils.GetComplianceState(managedPlc)
			}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		})
		It("should patch verify the patched label exists as expected", func() {
			By("Creating " + case9ConfigPolicyNameLabelCheck + " on managed")
			utils.Kubectl("apply", "-f", case9PolicyYamlLabelCheck, "-n", testNamespace)
			plc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigPolicy, case9ConfigPolicyNameLabelCheck, testNamespace, true, defaultTimeoutSeconds)
			Expect(plc).NotTo(BeNil())
			Eventually(func() interface{} {
				managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigPolicy, case9ConfigPolicyNameLabelCheck, testNamespace, true, defaultTimeoutSeconds)
				return utils.GetComplianceState(managedPlc)
			}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		})
	})
})
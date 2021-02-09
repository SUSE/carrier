package acceptance_test

import (
	"os"
	"path"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Apps", func() {
	var org = "apps-org"
	BeforeEach(func() {
		_, err := Carrier("create-org "+org, "")
		Expect(err).ToNot(HaveOccurred())
		_, err = Carrier("target "+org, "")
		Expect(err).ToNot(HaveOccurred())
	})
	Describe("push", func() {
		var appName string
		BeforeEach(func() {
			appName = "apps-" + strconv.Itoa(int(time.Now().Nanosecond()))
		})

		It("pushes an app successfully", func() {
			currentDir, err := os.Getwd()
			Expect(err).ToNot(HaveOccurred())
			appDir := path.Join(currentDir, "../sample-app")

			out, err := Carrier("push "+appName, appDir)
			Expect(err).ToNot(HaveOccurred(), out)
			out, err = Carrier("apps", "")
			Expect(err).ToNot(HaveOccurred(), out)
			Expect(out).To(MatchRegexp(appName + ".*|.*1/1.*|.*"))
		})
	})

	Describe("delete", func() {
		var appName string
		BeforeEach(func() {
			appName = "apps-" + strconv.Itoa(int(time.Now().Nanosecond()))
			currentDir, err := os.Getwd()
			Expect(err).ToNot(HaveOccurred())
			appDir := path.Join(currentDir, "../sample-app")
			out, err := Carrier("push "+appName, appDir)
			Expect(err).ToNot(HaveOccurred(), out)
		})

		It("deletes an app successfully", func() {
			_, err := Carrier("delete "+appName, "")
			Expect(err).ToNot(HaveOccurred())
			var out string
			Eventually(func() string {
				out, err = Carrier("apps", "")
				Expect(err).ToNot(HaveOccurred())
				return out
			}, "1m").ShouldNot(MatchRegexp(appName+".*|.*1/1.*|.*"), out)
		})
	})
})
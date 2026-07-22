package cache

/*
Copyright 2021 The KubeVela Authors.

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

import (
	"context"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCache(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cache Suite")
}

var _ = Describe("Test cache utils", func() {
	It("should return false for IsExpired()", func() {
		c := NewMemoryCache("test", 10*time.Hour)
		Expect(c.IsExpired()).Should(BeFalse())
	})

	It("test cache store", func() {
		store := NewMemoryCacheStore[string](context.TODO())
		store.Put("test", "test data", time.Second*2)
		store.Put("test2", "test data", 0)
		store.Put("test3", "test data", -1)
		time.Sleep(3 * time.Second)
		value, found := store.Get("test")
		Expect(value).Should(BeNil())
		Expect(found).Should(BeFalse())

		value, found = store.Get("test2")
		Expect(value).Should(Equal("test data"))
		Expect(found).Should(BeTrue())

		value, found = store.Get("test3")
		Expect(value).Should(Equal("test data"))
		Expect(found).Should(BeTrue())
	})

	It("test cache store delete key", func() {
		store := NewMemoryCacheStore[string](context.TODO())
		store.Put("test", "test data", time.Minute*2)
		store.Delete("test")
		value, found := store.Get("test")
		Expect(value).Should(BeNil())
		Expect(found).Should(BeFalse())
	})

	It("test cache store with multiple keys", func() {
		store := NewMemoryCacheStore[string](context.TODO())
		for i := 0; i < 100; i++ {
			key := "key-" + strconv.Itoa(i)
			store.Put(key, i, 0)
		}

		for i := 0; i < 100; i++ {
			key := "key-" + strconv.Itoa(i)
			value, found := store.Get(key)
			Expect(found).Should(BeTrue())
			Expect(value).Should(Equal(i))
		}
	})

	It("test cache store overwrite value", func() {
		store := NewMemoryCacheStore[string](context.TODO())
		store.Put("rw", "v1", time.Second)
		value, found := store.Get("rw")
		Expect(found).Should(BeTrue())
		Expect(value).Should(Equal("v1"))

		store.Put("rw", "v2", time.Second)
		value, found = store.Get("rw")
		Expect(found).Should(BeTrue())
		Expect(value).Should(Equal("v2"))
	})
})

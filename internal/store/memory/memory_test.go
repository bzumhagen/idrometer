package memory_test

import (
	"testing"

	"github.com/bzumhagen/idrometer/internal/domain"
	"github.com/bzumhagen/idrometer/internal/store/memory"
	"github.com/bzumhagen/idrometer/internal/store/storetest"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, func(*testing.T) domain.TxManager { return memory.New() })
}

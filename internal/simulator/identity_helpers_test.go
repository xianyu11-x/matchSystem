package simulator

import (
	"fmt"
	"strconv"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

// 测试标签对应固定数字，保持路由测试数据稳定。
func testNumericID(value string) uint64 {
	id, err := strconv.ParseUint(value, 10, 64)
	if value == "" {
		return 0
	}
	if err == nil {
		return id
	}
	fixtures := map[string]uint64{
		"test": 11495104353665842533, "builtin": 3387119344798129893,
		"default": 4010717984327297149, "p1": 17745680053102999587,
		"builtin-python": 8058128364639275194, "p2": 4127208643663288636,
		"staged": 10200773423553877654, "python": 1271323562368299593,
		"python-speed": 16322021703664149043, "reload": 4622933014161880904,
		"python-lifecycle": 5430550136017800594, "missing": 18421470166314807403,
		"new": 1247631298124474837, "second": 1600601599791221249,
		"remote": 13191494021638304619,
	}
	if id, ok := fixtures[value]; ok {
		return id
	}
	panic(fmt.Sprintf("unknown identity fixture %q", value))
}
func testNamespace(value string) string {
	return strconv.FormatUint(testNumericID(value), 10)
}
func testPhysicalID(value string) identity.PhysicalNodeID {
	return identity.PhysicalNodeID(testNumericID(value))
}
func testPlacementID(value string) identity.PlacementID {
	return identity.PlacementID(testNumericID(value))
}

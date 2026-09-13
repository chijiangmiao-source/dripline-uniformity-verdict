# 温室滴灌支路均匀度（DU）裁决服务

纯后端 API：接收一条滴灌支路上 4～64 个测点的实测流量，用**精确十进制（有理数）运算**
计算分布均匀度 DU（Distribution Uniformity of the Low Quarter），并给出唯一裁决。
即使各测点流量都接近标称值，最低的那一组也能暴露滴头堵塞。

- 语言/框架：Go 1.25 + Gin
- 精确运算：`math/big.Rat`，全程不经过 `float64`
- 编排：Docker Compose，常驻服务 `api` + 一次性验收服务 `verify`
- 端口：宿主端口由环境变量 `API_PORT` 覆盖（默认 `8080`）

---

## 1. 计算规则（可复算）

设样本数为 n：

1. 将全部测点按 `flow_lph` **升序**稳定排序，取前 `k = ceil(n/4)` 项作为最低组；
   流量并列时，**按输入次序**进入最低组（稳定排序）。
2. 精确计算：
   - 最低组均值 = 最低组流量之和 / k
   - 全体均值 = 全部流量之和 / n
   - `DU = 最低组均值 ÷ 全体均值 × 100`
3. 两个均值与 DU 全程保留为精确分数，**不提前舍入**；只有最终 DU 按十进制
   **ROUND_HALF_UP（四舍五入，遇五进一）保留两位小数**。
4. 裁决只依据舍入后的两位 DU，因此临界支路只有一个明确结论：

| 舍入后 DU（%） | 裁决 `verdict` | 中文 `verdict_text` |
|---|---|---|
| `>= 90.00` | `pass` | 通过 |
| `80.00` ～ `89.99` | `review` | 复查 |
| `< 80.00` | `fail` | 不通过 |

输入约束：

- `measurements` 长度 4～64；
- 每项 `id` 为**非空且唯一**的字符串；
- `flow_lph` 为 JSON 数字，普通十进制、最多三位小数，且 `0 < flow_lph <= 100`
  （拒绝科学计数法、字符串、前导零写法如 `01`、超过三位小数等）。
- `rated_flow_lph`（可选）为滴头额定流量，规则与 `flow_lph` 完全相同；
  **一次请求中必须全部测点提供或全部省略**，混用返回 422。

### 额定流量模式（混装不同额定流量滴头）

同一支路混装不同额定流量的滴头时，实测流量最低的测点未必是相对供水最不足的。
为每个测点补充 `rated_flow_lph` 后，验收改按**供给比 = 实测流量 ÷ 额定流量**裁决：

1. 每个测点以精确分数计算供给比（不经过浮点）；
2. 按供给比**升序**稳定排序，取前 `ceil(n/4)` 项为最低组（并列仍按输入次序）；
3. `DU = 低组供给比均值 ÷ 总体供给比均值 × 100`，最终舍入与三档阈值与上文一致。

额定模式下响应**保留全部原字段**（`lowest_mean_lph` / `overall_mean_lph` 仍为实测
流量均值，其中最低组由供给比选出），并增加三个字段：

| 字段 | 含义 |
|---|---|
| `calculation_basis` | 裁决依据，额定模式固定为 `"supply_ratio"` |
| `lowest_mean_ratio` | 最低组供给比均值（`decimal` / `exact_fraction` / `terminating` 契约同上） |
| `overall_mean_ratio` | 全体供给比均值，同上 |

省略 `rated_flow_lph` 的旧请求走实测流量模式，响应字段与历史版本**完全一致**
（不出现上述三个字段）。

---

## 2. HTTP 接口

### `POST /api/v1/verify`

请求示例：

```bash
curl -X POST http://localhost:8080/api/v1/verify \
  -H 'Content-Type: application/json' \
  -d '{
    "measurements": [
      {"id": "A-01", "flow_lph": 9.82},
      {"id": "A-02", "flow_lph": 9.95},
      {"id": "A-03", "flow_lph": 10.00},
      {"id": "A-04", "flow_lph": 10.05},
      {"id": "A-05", "flow_lph": 9.90},
      {"id": "A-06", "flow_lph": 10.10},
      {"id": "A-07", "flow_lph": 9.88},
      {"id": "A-08", "flow_lph": 10.02}
    ]
  }'
```

`n=8` 时 `k=2`，最低组为 `A-01(9.82)`、`A-07(9.88)`。成功响应（HTTP 200）：

```json
{
  "sample_count": 8,
  "lowest_count": 2,
  "lowest_ids": ["A-01", "A-07"],
  "lowest_mean_lph": {
    "decimal": "9.85",
    "exact_fraction": "197/20",
    "terminating": true
  },
  "overall_mean_lph": {
    "decimal": "9.965",
    "exact_fraction": "1993/200",
    "terminating": true
  },
  "du_percent": {
    "rounded": "98.85",
    "exact_fraction": "197000/1993"
  },
  "verdict": "pass",
  "verdict_text": "通过"
}
```

字段说明：

| 字段 | 含义 |
|---|---|
| `sample_count` | 样本数 n |
| `lowest_count` | 最低组数量 `ceil(n/4)` |
| `lowest_ids` | 最低组测点 id（按流量升序、并列按输入次序） |
| `lowest_mean_lph` | 最低组均值，**未提前舍入**（见下方精确值对象） |
| `overall_mean_lph` | 全体均值，同上 |
| `du_percent.rounded` | 最终 DU，全响应唯一被舍入的值，ROUND_HALF_UP 后固定两位小数 |
| `du_percent.exact_fraction` | 未提前舍入的精确 DU 分数 `num/den` |
| `verdict` / `verdict_text` | 机器可读裁决 / 中文裁决 |

**精确值对象（`*_lph`）三字段：**

- `exact_fraction`：权威值，既约分数 `num/den`，携带全部精度。
- `decimal`：便于人读的十进制文本。
- `terminating`：`true` 表示该均值是有限小数，`decimal` 与分数**完全相等**；
  `false` 表示是循环小数，`decimal` 只是 12 位 ROUND_HALF_UP 预览，
  **复算必须使用 `exact_fraction`**。

### 仅凭响应精确复算（含循环小数）

均值分母可能含 2、5 以外的质因子（例如 7 个测点除以 7），此时十进制写不尽。
响应始终附精确分数，验收方无需访问服务端即可独立复算，例如 7 测点（一个
`99.999`、六个 `100`）：

```json
"lowest_mean_lph":  { "decimal": "99.9995",          "exact_fraction": "199999/2000", "terminating": true  },
"overall_mean_lph": { "decimal": "99.999857142857",  "exact_fraction": "699999/7000",  "terminating": false },
"du_percent":       { "rounded": "100.00",            "exact_fraction": "69999650/699999" }
```

复算（全程分数运算，最后才四舍五入）：

```
DU = (199999/2000) ÷ (699999/7000) × 100 = 69999650/699999
   → ROUND_HALF_UP 两位 = 100.00 → pass
```

可见 `overall_mean_lph.decimal` 的 12 位只是展示，真正参与复算的是
`exact_fraction`，因此临界支路不会因均值被截断而产生二义结论。

### 额定模式请求与响应

```bash
curl -X POST http://localhost:8080/api/v1/verify \
  -H 'Content-Type: application/json' \
  -d '{
    "measurements": [
      {"id": "R-01", "flow_lph": 8,    "rated_flow_lph": 8},
      {"id": "R-02", "flow_lph": 9,    "rated_flow_lph": 18},
      {"id": "R-03", "flow_lph": 10,   "rated_flow_lph": 10},
      {"id": "R-04", "flow_lph": 10,   "rated_flow_lph": 10}
    ]
  }'
```

`R-02` 实测流量（9）并非最低，但供给比 `9/18 = 0.5` 最低，进入最低组：

```json
{
  "sample_count": 4,
  "lowest_count": 1,
  "lowest_ids": ["R-02"],
  "lowest_mean_lph": {"decimal": "9", "exact_fraction": "9/1", "terminating": true},
  "overall_mean_lph": {"decimal": "9.25", "exact_fraction": "37/4", "terminating": true},
  "du_percent": {"rounded": "57.14", "exact_fraction": "400/7"},
  "verdict": "fail",
  "verdict_text": "不通过",
  "calculation_basis": "supply_ratio",
  "lowest_mean_ratio": {"decimal": "0.5", "exact_fraction": "1/2", "terminating": true},
  "overall_mean_ratio": {"decimal": "0.875", "exact_fraction": "7/8", "terminating": true}
}
```

额定模式下复算改用两个 `*_ratio` 的 `exact_fraction`：
`DU = (1/2) ÷ (7/8) × 100 = 400/7 → 57.14 → fail`。

### 错误响应

数量、id 或流量任一非法，返回 **HTTP 422**，并定位**首个**出错字段路径；整次请求
**不输出任何部分均值或 DU**：

```bash
curl -i -X POST http://localhost:8080/api/v1/verify \
  -H 'Content-Type: application/json' \
  -d '{"measurements":[
        {"id":"A","flow_lph":10},
        {"id":"B","flow_lph":0},
        {"id":"C","flow_lph":10},
        {"id":"D","flow_lph":10}]}'
```

```
HTTP/1.1 422 Unprocessable Entity
```
```json
{
  "error": {
    "message": "flow_lph must be greater than 0 and at most 100",
    "field": "measurements[1].flow_lph"
  }
}
```

字段路径形如 `measurements`、`measurements[2].id`、`measurements[3].flow_lph`、
`measurements[1].rated_flow_lph`。`rated_flow_lph` 只在一部分测点出现时（混用），
同样返回 422，并定位**首个漏填该字段的测点**——例如首个测点未填而后续测点已填：

```bash
curl -i -X POST http://localhost:8080/api/v1/verify \
  -H 'Content-Type: application/json' \
  -d '{"measurements":[
        {"id":"A","flow_lph":10},
        {"id":"B","flow_lph":10,"rated_flow_lph":8},
        {"id":"C","flow_lph":10,"rated_flow_lph":8},
        {"id":"D","flow_lph":10,"rated_flow_lph":8}]}'
```

```
HTTP/1.1 422 Unprocessable Entity
```
```json
{
  "error": {
    "message": "rated_flow_lph must be provided for every measurement or omitted for all",
    "field": "measurements[0].rated_flow_lph"
  }
}
```

请求体不是合法 JSON 对象时返回 400。

### 健康检查

`GET /healthz` → `200 {"status":"ok"}`

---

## 3. Docker Compose

### 常驻 API

```bash
# 默认宿主端口 8080
docker compose up -d --build api

# 覆盖宿主端口（容器内监听端口同步为该值）
API_PORT=9090 docker compose up -d --build api
curl http://localhost:9090/healthz
```

### 一次性验收服务 `verify`

`verify` 是**一次性**服务：等待 `api` 健康后，只提交一次验收请求
（默认读取容器内 `/data/payload.json`），打印裁决并退出，退出码即验收结论。
加 `--abort-on-container-exit --exit-code-from verify` 可在它退出后一并停止
`api`，并把验收结论作为 compose 命令的退出码：

```bash
docker compose up --build --abort-on-container-exit --exit-code-from verify verify
echo "验收退出码: $?"   # 0 通过 / 1 不通过 / 2 复查
# 默认挂载的是 examples/acceptance-pass.json
```

换成待验收支路（二选一）：

```bash
# 1) 修改挂载的载荷文件
docker compose run --rm \
  -v "$PWD/examples/acceptance-review.json:/data/payload.json:ro" verify

# 2) 直接管道传入 JSON（令载荷路径不存在，即回退到标准输入）
cat my-branch.json | docker compose run --rm -T \
  -e VERIFY_PAYLOAD=/data/none.json verify
```

`verify` 退出码：

| 退出码 | 含义 |
|---|---|
| 0 | 通过（pass） |
| 1 | 不通过（fail） |
| 2 | 复查（review） |
| 3 | 请求被 API 判为非法（422） |
| 4 | 其他非预期上游响应 |
| 5 | API 不可达 / 等待健康超时 |

可用环境变量：`API_BASE_URL`（默认 `http://api:8080`）、`VERIFY_PAYLOAD`
（默认 `/data/payload.json`）。

### 示例载荷

`examples/` 下提供三档示例，裁决分别为通过 / 复查 / 不通过：

```bash
curl -s -X POST http://localhost:8080/api/v1/verify \
  -H 'Content-Type: application/json' \
  --data @examples/acceptance-fail.json
# ... "du_percent": {"rounded":"66.64", ...}, "verdict":"fail","verdict_text":"不通过"
```

另有 `acceptance-rated.json`：同一支路混装 8 与 16 LPH 两种额定滴头，
按供给比裁决（`calculation_basis: "supply_ratio"`，DU 89.16 → 复查）。

---

## 4. 本地开发与测试

需要 Go 1.25。

```bash
go test ./...            # 单元测试（testify）
go test -race ./...      # 竞态检测
go vet ./...

go run ./cmd/api                       # 启动 API（默认 :8080）
API_PORT=9090 go run ./cmd/api         # 指定端口
echo '{"measurements":[{"id":"a","flow_lph":2},{"id":"b","flow_lph":10},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}' \
  | go run ./cmd/verify                # 对已运行的 API 做一次性验收
```

关键边界均被测试覆盖：`n=4/5/…/64` 的 `ceil(n/4)`、并列最低值按输入次序、
DU 精确等于 `90.005 / 89.995 / 90.00 / 80.00 / 79.99` 时的 ROUND_HALF_UP 与档位归属、
循环小数均值不提前舍入、非法数量/空 id/重复 id/越界与超精度流量的首个字段定位、
额定模式下不同额定值改变最低组、并列供给比保持输入次序、循环供给比均值的精确复算、
`rated_flow_lph` 混用与非法值的首个字段定位，以及旧样例响应逐字段不变。

## 目录结构

```
cmd/api/            常驻 HTTP 服务入口（读取 API_PORT）
cmd/verify/         一次性验收客户端入口
internal/dripdu/    十进制解析、精确 DU 计算与裁决（核心领域逻辑）
internal/httpapi/   Gin 路由、请求校验（首个字段路径）、响应
internal/verifyclient/ 一次性验收的等待就绪、调用与退出码映射
examples/           通过/复查/不通过三份请求示例
Dockerfile          多阶段构建，同一镜像含 api 与 verify 两个命令
docker-compose.yml  api（常驻）+ verify（一次性）
```

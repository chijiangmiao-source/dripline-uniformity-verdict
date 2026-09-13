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
  "lowest_mean_lph": "9.85",
  "overall_mean_lph": "9.965",
  "du_percent": "98.85",
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
| `lowest_mean_lph` | 最低组均值，**未提前舍入**；有限小数原样输出，无限循环小数保留 12 位仅供传输（DU 始终由精确分数计算，不使用此文本） |
| `overall_mean_lph` | 全体均值，同上 |
| `du_percent` | 最终 DU，ROUND_HALF_UP 后固定两位小数 |
| `verdict` / `verdict_text` | 机器可读裁决 / 中文裁决 |

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

字段路径形如 `measurements`、`measurements[2].id`、`measurements[3].flow_lph`。
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
# ... "du_percent":"66.64","verdict":"fail","verdict_text":"不通过"
```

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
循环小数均值不提前舍入、非法数量/空 id/重复 id/越界与超精度流量的首个字段定位等。

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

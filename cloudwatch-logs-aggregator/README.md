# cloudwatch-logs-aggregator
cloudwatch-logs-aggregator aggregates logs stored in [Amazon CloudWatch Logs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/WhatIsCloudWatchLogs.html) into metrics, and post those metrics to Mackerel.

cloudwatch-logs-aggregator consists of two Terraform modules:

- [cloudwatch-logs-aggregator-lambda](#cloudwatch-logs-aggregator-lambda)
- [cloudwatch-logs-aggregator-rule](#cloudwatch-logs-aggregator-rule)

```
               /------\     /--------\
Log Group ====>| Rule |====>|        |
               \------/     |        |
                            |        |
               /------\     |        |
Log Group ====>| Rule |====>| Lambda |====> Mackerel 
               \------/     |        |
                            |        |
               /------\     |        |
Log Group ====>| Rule |====>|        |
               \------/     \--------/
```

## cloudwatch-logs-aggregator-lambda
The cloudwatch-logs-aggregator-lambda module manages a Lambda function that runs CloudWatch Logs Insights queries and posts aggregated metrics to Mackerel.

### Example
``` hcl
module "cw_logs_aggregator_lambda" {
  source = "github.com/mackerelio-labs/mackerel-monitoring-modules//cloudwatch-logs-aggregator/lambda?ref=v0.1.0"

  region = "ap-northeast-1"
}
```

### Variables
| Name | Description | Default |
| --- | --- | --- |
| `region` | The AWS region in which the resources are created. | |
| `iam_role_name` | The name of the Lambda function's execution role. | `"mackerel-cloudwatch-logs-aggregator-lambda"` |
| `function_name` | The name of the Lambda function. | `"mackerel-cloudwatch-logs-aggregator"` |
| `memory_size_in_mb` | The memory size of the Lambda function, in megabytes. | `128` |
| `timeout_in_seconds` | The maximum execution time of the Lambda function, in seconds. | `60` |
| `log_retention_in_days` | The retention of the Lambda function's log group, in days. | `0` (never expire) |

## cloudwatch-logs-aggregator-rule
The cloudwatch-logs-aggregator-rule module manages an EventBridge rule that periodically dispatches events to a Lambda function created by the cloudwatch-logs-aggregator-lambda module.

You can attach multiple rules for a signle Lambda function.

### Example
``` hcl
module "cw_logs_aggregator_rule_batch_jobs" {
  source = "github.com/mackerelio-labs/mackerel-monitoring-modules//cloudwatch-logs-aggregator/rule?ref=v0.1.0"

  region       = "ap-northeast-1"
  rule_name    = "mackerel-cloudwatch-logs-aggregator-batch-jobs"
  function_arn = module.cw_logs_aggregator_lambda.function_arn

  # Mackerel settings
  api_key_name = "/mackerel/apiKey"
  service_name = "my-service"

  # Query settings
  log_group_name      = "/my/log/group"
  query               = "stats sum(count) as count by job_name"
  metric_name_prefix  = "batch-job"
  group_field         = "job_name"
  schedule_expression = "rate(5 minutes)"
  interval_in_minutes = 5
  offset_in_minutes   = 10
}
```

### Variables
| Name | Description | Default |
| --- | --- | --- |
| `region` | The AWS region in which the resources are created. | |
| `rule_name` | The name of the EventBridge rule. | |
| `function_arn` | The ARN of the target Lambda function. | |
| `is_enabled` | Whether the rules is enabled or not. | `true` |
| `api_key_name` | The name of the parameter of Parameter Store which stores the Mackerel API key. It is recommended to store the API key as a secure string. | |
| `service_name` | The name of the service on Mackerel to which metrics are posted. | |
| `log_group_name` | The name of the log group to be aggregated. | |
| `query` | The CloudWatch Logs Insights query that aggregates logs. See [the document](https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/CWL_QuerySyntax.html) for the query syntax. | |
| `metric_name_prefix` | The common prefix appended to metric names. See the [Metrics](#metrics) section below. | `""` |
| `group_field` | The field name that is used to group metrics. See the [Metrics](#metrics) section below. | `""` |
| `default_field` | The field name that is not included in metric names. See the [Metrics](#metrics) section below. | `""` |
| `schedule_expression` | The schedule expression of the rule specifying the execution interval. Usually the execution interval is equal to the query interval. See [the document](https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-create-rule-schedule.html) for the expression syntax. | |
| `interval_in_minutes` | The interval of the query time range, in minutes. | |
| `offset_in_minutes` | The offset of the query time range, in minutes. Usually this is the assumed maximum delay of logs. | `10` |

### Metrics
Each field in the query result yields a metric data point.

The metric name is obtained by joining the following names with dots `.`:

- The value of `metric_name_prefix`, if exists
- The value of the field specified by `group_field`, if exists
- The name of the field, if `default_field` does not match

The metric value is the value of the field.

## Examples
### Simple logs
Logs:

``` json
{"processed":18,"success":15,"failure":3}
{"processed":12,"success":10,"failure":2}
```

Query:

```
stats sum(processed) as processed, sum(success) as success, sum(failure) as failure
```

Query result:

```
| processed | success | failure |
| --------- | ------- | ------- |
|        30 |      25 |       5 |
```

<table>
<thead>
<tr>
<th>Rule settings</th>
<th>Metrics</th>
</tr>
</thead>
<tbody>
<tr>
<td>

``` hcl
metric_name_prefix = ""
group_field        = ""
default_field      = ""
```

</td>
<td>

```
processed	30	<timestamp>
success	25	<timestamp>
failure	5	<timestamp>
```

</td>
</tr>
<tr>
<td>

``` hcl
metric_name_prefix = "my-batch-job"
group_field        = ""
default_field      = ""
```

</td>
<td>

```
my-batch-job.processed	30	<timestamp>
my-batch-job.success	25	<timestamp>
my-batch-job.failure	5	<timestamp>
```

</td>
</tr>
<tr>
<td>

``` hcl
metric_name_prefix = "my-batch-job"
group_field        = ""
default_field      = "processed"
```

</td>
<td>

```
my-batch-job	30	<timestamp>
my-batch-job.success	25	<timestamp>
my-batch-job.failure	5	<timestamp>
```

</td>
</tr>
</tbody>
</table>

### Logs with labels
Logs:

``` json
{"job_name":"foo","processed":18,"success":15,"failure":3}
{"job_name":"bar","processed":3,"success":3,"failure":0}
{"job_name":"foo","processed":12,"success":10,"failure":2}
{"job_name":"bar","processed":5,"success":3,"failure":2}
```

Query:

```
stats sum(processed) as processed, sum(success) as success, sum(failure) as failure by job_name
```

Query result:

```
| job_name | processed | success | failure |
| ---------| --------- | ------- | ------- |
| foo      |        30 |      25 |       5 |
| bar      |         8 |       6 |       2 |
```


<table>
<thead>
<tr>
<th>Rule settings</th>
<th>Metrics</th>
</tr>
</thead>
<tbody>
<tr>
<td>

``` hcl
metric_name_prefix = ""
group_field        = "job_name"
default_field      = ""
```

</td>
<td>

```
foo.processed	30	<timestamp>
foo.success	25	<timestamp>
foo.failure	5	<timestamp>
bar.processed	8	<timestamp>
bar.success	6	<timestamp>
bar.failure	2	<timestamp>
```

</td>
</tr>
<tr>
<td>

``` hcl
metric_name_prefix = "my-batch-job"
group_field        = "job_name"
default_field      = ""
```

</td>
<td>

```
my-batch-job.foo.processed	30	<timestamp>
my-batch-job.foo.success	25	<timestamp>
my-batch-job.foo.failure	5	<timestamp>
my-batch-job.bar.processed	8	<timestamp>
my-batch-job.bar.success	6	<timestamp>
my-batch-job.bar.failure	2	<timestamp>
```

</td>
</tr>
<tr>
<td>

``` hcl
metric_name_prefix = ""
group_field        = "job_name"
default_field      = "processed"
```

</td>
<td>

```
foo	30	<timestamp>
foo.success	25	<timestamp>
foo.failure	5	<timestamp>
bar	8	<timestamp>
bar.success	6	<timestamp>
bar.failure	2	<timestamp>
```

</td>
</tr>
</tbody>
</table>

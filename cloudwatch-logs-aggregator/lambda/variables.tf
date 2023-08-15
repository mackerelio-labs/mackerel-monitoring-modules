variable "region" {
  description = "The AWS region in which the resources are created."
  type        = string
}

variable "iam_role_name" {
  description = "The name of the Lambda function's execution role."
  type        = string
  default     = "mackerel-cloudwatch-logs-aggregator-lambda"
}

variable "function_name" {
  description = "The name of the Lambda function."
  type        = string
  default     = "mackerel-cloudwatch-logs-aggregator"
}

variable "memory_size_in_mb" {
  description = "The memory size of the Lambda function, in megabytes."
  type        = number
  default     = 128
}

variable "timeout_in_seconds" {
  description = "The maximum execution time of the Lambda function, in seconds."
  type        = number
  default     = 60
}

variable "log_retention_in_days" {
  description = "The retention of the Lambda function's log group, in days."
  type        = number
  default     = 0
}

variable "tags" {
  description = "Tags of the Lambda function"
  type        = map(any)
  default     = {}
}

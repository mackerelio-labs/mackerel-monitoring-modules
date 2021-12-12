variable "region" {
  description = "The AWS region in which the resources are created."
  type        = string
}

variable "rule_name" {
  description = "The name of the EventBridge rule."
  type        = string
}

variable "is_enabled" {
  description = "Whether the rules is enabled or not."
  type        = bool
  default     = true
}

variable "schedule_expression" {
  description = "The schedule expression of the rule specifying the execution interval. Usually the execution interval is equal to the query interval."
  type        = string
}

variable "function_arn" {
  description = "The ARN of the target Lambda function."
  type        = string
}

variable "api_base_url" {
  description = "The base URL used for API requests to Mackerel."
  type        = string
  default     = ""
}

variable "api_key_name" {
  description = "The name of the parameter of Parameter Store which stores the Mackerel API key."
  type        = string
  validation {
    condition     = length(var.api_key_name) > 0
    error_message = "The api_key_name value is required."
  }
}

variable "log_group_name" {
  description = "The name of the log group to be aggregated."
  type        = string
  validation {
    condition     = length(var.log_group_name) > 0
    error_message = "The log_group_name value is required."
  }
}

variable "query" {
  description = "The CloudWatch Logs Insights query that aggregates logs."
  type        = string
  validation {
    condition     = length(var.query) > 0
    error_message = "The query value is required."
  }
}

variable "interval_in_minutes" {
  description = "The interval of the query time range, in minutes."
  type        = number
  validation {
    condition     = var.interval_in_minutes > 0
    error_message = "The interval_in_minutes value must be positive."
  }
}

variable "offset_in_minutes" {
  description = "The offset of the query time range, in minutes. Usually this is the assumed maximum delay of logs."
  type        = number
  default     = 10
  validation {
    condition     = var.offset_in_minutes >= 0
    error_message = "The offset_in_minutes value must be zero or positive."
  }
}

variable "metric_name_prefix" {
  description = "The common prefix appended to metric names."
  type        = string
  default     = ""
}

variable "group_field" {
  description = "The field name that is used to group metrics."
  type        = string
  default     = ""
}

variable "default_field" {
  description = "The field name that is not included in metric names."
  type        = string
  default     = ""
}

variable "default_metrics" {
  description = "The default metrics posted when the corresponding metrics are missing."
  type        = map(number)
  default     = {}
}

variable "service_name" {
  description = "The name of the service on Mackerel to which metrics are posted."
  type        = string
  validation {
    condition     = length(var.service_name) > 0
    error_message = "The service_name value is required."
  }
}

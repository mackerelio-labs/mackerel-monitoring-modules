provider "aws" {
  region = var.region
}

resource "aws_cloudwatch_event_rule" "this" {
  name        = var.rule_name
  description = "created by mackerel-monitoring-modules"
  // TODO we will follow upstream?
  state               = var.is_enabled
  schedule_expression = var.schedule_expression
}

resource "aws_cloudwatch_event_target" "this" {
  rule = aws_cloudwatch_event_rule.this.name
  arn  = var.function_arn

  input = jsonencode({
    rule_name = aws_cloudwatch_event_rule.this.name

    api_base_url = var.api_base_url
    api_key_name = var.api_key_name

    log_group_name      = var.log_group_name
    query               = var.query
    interval_in_minutes = var.interval_in_minutes
    offset_in_minutes   = var.offset_in_minutes

    metric_name_prefix = var.metric_name_prefix
    group_field        = var.group_field
    default_field      = var.default_field
    default_metrics    = var.default_metrics

    service_name = var.service_name
  })
}

resource "aws_lambda_permission" "this" {
  statement_id  = var.rule_name
  action        = "lambda:InvokeFunction"
  function_name = var.function_arn
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.this.arn
}

terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.27.0"
    }
  }
}

resource "aws_iam_role" "this" {
  name        = var.iam_role_name
  description = "created by mackerel-monitoring-modules"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = "sts:AssumeRole"
        Principal = {
          Service = "lambda.amazonaws.com"
        }
      },
    ]
  })

  managed_policy_arns = ["arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"]
}

resource "aws_iam_role_policy" "this" {
  role   = aws_iam_role.this.id
  name   = "cloudwatch-logs-aggregator-lambda"
  policy = data.aws_iam_policy_document.this.json
}

data "aws_iam_policy_document" "this" {
  version = "2012-10-17"
  statement {
    effect    = "Allow"
    actions   = ["ssm:GetParameter"]
    resources = ["*"]
  }
  statement {
    effect = "Allow"
    actions = [
      "logs:StartQuery",
      "logs:StopQuery",
      "logs:GetQueryResults",
    ]
    resources = ["*"]
  }
}

locals {
  function_zip = "${path.module}/function.zip"
}

resource "aws_lambda_function" "this" {
  function_name = var.function_name
  description   = "created by mackerel-monitoring-modules"
  role          = aws_iam_role.this.arn

  runtime     = "provided.al2"
  memory_size = var.memory_size_in_mb
  timeout     = var.timeout_in_seconds
  filename    = local.function_zip
  handler     = "main"

  source_code_hash = filebase64sha256(local.function_zip)

  depends_on = [aws_cloudwatch_log_group.this]
  tags       = var.tags
}

resource "aws_cloudwatch_log_group" "this" {
  name              = "/aws/lambda/${var.function_name}"
  retention_in_days = var.log_retention_in_days
}

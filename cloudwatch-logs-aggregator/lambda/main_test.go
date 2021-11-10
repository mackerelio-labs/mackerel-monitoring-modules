package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go/service/ssm"
	"github.com/mackerelio/mackerel-client-go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

var logger = logrus.New()

func init() {
	logger.SetFormatter(&logrus.JSONFormatter{})
	logger.SetLevel(logrus.DebugLevel)
}

var baseEvent = Event{
	APIKeyName:        "/mackerel/apiKey",
	LogGroupName:      "/aws/lambda/my-function",
	Query:             "filter ... | stats ...",
	IntervalInMinutes: 5,
	OffsetInMinutes:   10,
	ServiceName:       "my-service",
}

func TestValidateInput_returns_no_error_if_valid(t *testing.T) {
	err := ValidateInput(&baseEvent)
	assert.NoError(t, err)
}

func TestValidateInput_ApiKeyName_is_required(t *testing.T) {
	event := baseEvent
	event.APIKeyName = ""
	err := ValidateInput(&event)
	assert.EqualError(t, err, "api_key_name is required")
}

func TestValidateInput_LogGroupName_is_required(t *testing.T) {
	event := baseEvent
	event.LogGroupName = ""
	err := ValidateInput(&event)
	assert.EqualError(t, err, "log_group_name is required")
}

func TestValidateInput_Query_is_required(t *testing.T) {
	event := baseEvent
	event.Query = ""
	err := ValidateInput(&event)
	assert.EqualError(t, err, "query is required")
}

func TestValidateInput_IntervalInMinutes_must_be_positive(t *testing.T) {
	event := baseEvent

	event.IntervalInMinutes = 1
	err1 := ValidateInput(&event)
	assert.NoError(t, err1)

	event.IntervalInMinutes = 0
	err2 := ValidateInput(&event)
	assert.EqualError(t, err2, "interval_in_minutes must be positive")

	event.IntervalInMinutes = -1
	err3 := ValidateInput(&event)
	assert.EqualError(t, err3, "interval_in_minutes must be positive")
}

func TestValidateInput_OffsetInMinutes_must_be_zero_or_positive(t *testing.T) {
	event := baseEvent

	event.OffsetInMinutes = 1
	err1 := ValidateInput(&event)
	assert.NoError(t, err1)

	event.OffsetInMinutes = 0
	err2 := ValidateInput(&event)
	assert.NoError(t, err2)

	event.OffsetInMinutes = -1
	err3 := ValidateInput(&event)
	assert.EqualError(t, err3, "offset_in_minutes must be zero or positive")
}

func TestValidateInput_ServiceName_is_required(t *testing.T) {
	event := baseEvent
	event.ServiceName = ""
	err := ValidateInput(&event)
	assert.EqualError(t, err, "service_name is required")
}

func TestGetQueryTimeRange_returns_rounded_time_range(t *testing.T) {
	currentTime := time.Date(2021, time.June, 6, 12, 34, 56, 789, time.UTC)
	timeRange := GetQueryTimeRange(currentTime, 5*time.Minute, 7*time.Minute)
	assert.Equal(t, time.Date(2021, time.June, 6, 12, 20, 0, 0, time.UTC), timeRange.StartTime)
	assert.Equal(t, time.Date(2021, time.June, 6, 12, 25, 0, 0, time.UTC), timeRange.EndTime)
}

type mockSSMService struct {
	getParameterWithContext func(ctx aws.Context, input *ssm.GetParameterInput, opts ...request.Option) (*ssm.GetParameterOutput, error)
}

func (svc *mockSSMService) GetParameterWithContext(ctx aws.Context, input *ssm.GetParameterInput, opts ...request.Option) (*ssm.GetParameterOutput, error) {
	return svc.getParameterWithContext(ctx, input, opts...)
}

func TestGetAPIKey_returns_apiKey_retrieved_from_parameter_store(t *testing.T) {
	ctx := context.Background()
	var getParameterInput *ssm.GetParameterInput
	svc := &mockSSMService{
		getParameterWithContext: func(ctx aws.Context, input *ssm.GetParameterInput, opts ...request.Option) (*ssm.GetParameterOutput, error) {
			getParameterInput = input
			return &ssm.GetParameterOutput{
				Parameter: &ssm.Parameter{
					Value: aws.String("foobar"),
				},
			}, nil
		},
	}
	apiKeyName := "/mackerel/apiKey"

	apiKey, err := GetAPIKey(ctx, svc, apiKeyName)

	assert.Equal(t, apiKeyName, *getParameterInput.Name)
	assert.True(t, *getParameterInput.WithDecryption)

	assert.NoError(t, err)
	assert.Equal(t, "foobar", apiKey)
}

func TestCreateMackerelClient_creates_new_client(t *testing.T) {
	client, err := CreateMackerelClient("", "foobar")
	assert.NoError(t, err)
	assert.Equal(t, "foobar", client.APIKey)
	assert.Equal(t, "mackerel-cloudwatch-logs-aggregator/"+version, client.UserAgent)
}

func TestCreateMackerelClient_set_base_url(t *testing.T) {
	apiBaseURL := "https://example.com/"
	client, err := CreateMackerelClient(apiBaseURL, "foobar")
	assert.NoError(t, err)
	assert.Equal(t, apiBaseURL, client.BaseURL.String())
	assert.Equal(t, "foobar", client.APIKey)
	assert.Equal(t, "mackerel-cloudwatch-logs-aggregator/"+version, client.UserAgent)
}

type mockCWLogsService struct {
	startQueryWithContext      func(ctx aws.Context, input *cloudwatchlogs.StartQueryInput, opts ...request.Option) (*cloudwatchlogs.StartQueryOutput, error)
	stopQueryWithContext       func(ctx aws.Context, input *cloudwatchlogs.StopQueryInput, opts ...request.Option) (*cloudwatchlogs.StopQueryOutput, error)
	getQueryResultsWithContext func(ctx aws.Context, input *cloudwatchlogs.GetQueryResultsInput, opts ...request.Option) (*cloudwatchlogs.GetQueryResultsOutput, error)
}

func (svc *mockCWLogsService) StartQueryWithContext(ctx aws.Context, input *cloudwatchlogs.StartQueryInput, opts ...request.Option) (*cloudwatchlogs.StartQueryOutput, error) {
	return svc.startQueryWithContext(ctx, input, opts...)
}

func (svc *mockCWLogsService) StopQueryWithContext(ctx aws.Context, input *cloudwatchlogs.StopQueryInput, opts ...request.Option) (*cloudwatchlogs.StopQueryOutput, error) {
	return svc.stopQueryWithContext(ctx, input, opts...)
}

func (svc *mockCWLogsService) GetQueryResultsWithContext(ctx aws.Context, input *cloudwatchlogs.GetQueryResultsInput, opts ...request.Option) (*cloudwatchlogs.GetQueryResultsOutput, error) {
	return svc.getQueryResultsWithContext(ctx, input, opts...)
}

func TestRunQuery_returns_query_result_after_complete(t *testing.T) {
	ctx := context.Background()
	var startQueryInput *cloudwatchlogs.StartQueryInput
	var getQueryResultsInput *cloudwatchlogs.GetQueryResultsInput
	queryID := "my-query-id"
	index := 0
	outputs := []*cloudwatchlogs.GetQueryResultsOutput{
		{
			Status: aws.String(cloudwatchlogs.QueryStatusScheduled),
		},
		{
			Status: aws.String(cloudwatchlogs.QueryStatusRunning),
		},
		{
			Status: aws.String(cloudwatchlogs.QueryStatusComplete),
			Results: [][]*cloudwatchlogs.ResultField{
				{
					{
						Field: aws.String("group"),
						Value: aws.String("foobar"),
					},
					{
						Field: aws.String("total"),
						Value: aws.String("123"),
					},
				},
			},
			Statistics: &cloudwatchlogs.QueryStatistics{
				BytesScanned:   aws.Float64(30.0),
				RecordsScanned: aws.Float64(20.0),
				RecordsMatched: aws.Float64(10.0),
			},
		},
	}
	svc := &mockCWLogsService{
		startQueryWithContext: func(ctx aws.Context, input *cloudwatchlogs.StartQueryInput, opts ...request.Option) (*cloudwatchlogs.StartQueryOutput, error) {
			startQueryInput = input
			return &cloudwatchlogs.StartQueryOutput{QueryId: aws.String(queryID)}, nil
		},
		getQueryResultsWithContext: func(ctx aws.Context, input *cloudwatchlogs.GetQueryResultsInput, opts ...request.Option) (*cloudwatchlogs.GetQueryResultsOutput, error) {
			getQueryResultsInput = input
			i := index
			index += 1
			return outputs[i], nil
		},
	}
	logGroupName := "/aws/lambda/my-function"
	query := "filter ... | stats ..."
	timeRange := &QueryTimeRange{
		StartTime: time.Date(2021, time.June, 6, 12, 20, 0, 0, time.UTC),
		EndTime:   time.Date(2021, time.June, 6, 12, 25, 0, 0, time.UTC),
	}

	result, err := RunQuery(ctx, logger, svc, logGroupName, query, timeRange)

	assert.Equal(t, logGroupName, *startQueryInput.LogGroupName)
	assert.Equal(
		t,
		"filter 1622982000000 <= tomillis(@timestamp) and tomillis(@timestamp) < 1622982300000 | "+query,
		*startQueryInput.QueryString,
	)
	assert.Equal(t, timeRange.StartTime.Unix(), *startQueryInput.StartTime)
	assert.Equal(t, timeRange.EndTime.Unix(), *startQueryInput.EndTime)
	assert.Equal(t, queryID, *getQueryResultsInput.QueryId)

	assert.NoError(t, err)
	assert.Equal(t, cloudwatchlogs.QueryStatusComplete, *result.Status)
	if assert.Len(t, result.Results, 1) {
		if assert.Len(t, result.Results[0], 2) {
			assert.Equal(t, "group", *result.Results[0][0].Field)
			assert.Equal(t, "foobar", *result.Results[0][0].Value)
			assert.Equal(t, "total", *result.Results[0][1].Field)
			assert.Equal(t, "123", *result.Results[0][1].Value)
		}
	}
	assert.Equal(t, 30.0, *result.Statistics.BytesScanned)
	assert.Equal(t, 20.0, *result.Statistics.RecordsScanned)
	assert.Equal(t, 10.0, *result.Statistics.RecordsMatched)
}

func TestRunQuery_retries_if_failed_to_get_query_result(t *testing.T) {
	ctx := context.Background()
	queryID := "my-query-id"
	index := 0
	outputs := []*cloudwatchlogs.GetQueryResultsOutput{
		nil,
		{
			Status: aws.String(cloudwatchlogs.QueryStatusComplete),
			Results: [][]*cloudwatchlogs.ResultField{
				{
					{
						Field: aws.String("group"),
						Value: aws.String("foobar"),
					},
					{
						Field: aws.String("total"),
						Value: aws.String("123"),
					},
				},
			},
			Statistics: &cloudwatchlogs.QueryStatistics{
				BytesScanned:   aws.Float64(30.0),
				RecordsScanned: aws.Float64(20.0),
				RecordsMatched: aws.Float64(10.0),
			},
		},
	}
	svc := &mockCWLogsService{
		startQueryWithContext: func(ctx aws.Context, input *cloudwatchlogs.StartQueryInput, opts ...request.Option) (*cloudwatchlogs.StartQueryOutput, error) {
			return &cloudwatchlogs.StartQueryOutput{QueryId: aws.String(queryID)}, nil
		},
		getQueryResultsWithContext: func(ctx aws.Context, input *cloudwatchlogs.GetQueryResultsInput, opts ...request.Option) (*cloudwatchlogs.GetQueryResultsOutput, error) {
			i := index
			index += 1
			if outputs[i] == nil {
				return nil, errors.New("test error")
			}
			return outputs[i], nil
		},
	}
	logGroupName := "/aws/lambda/my-function"
	query := "filter ... | stats ..."
	timeRange := &QueryTimeRange{
		StartTime: time.Date(2021, time.June, 6, 12, 20, 0, 0, time.UTC),
		EndTime:   time.Date(2021, time.June, 6, 12, 25, 0, 0, time.UTC),
	}

	result, err := RunQuery(ctx, logger, svc, logGroupName, query, timeRange)

	assert.NoError(t, err)
	assert.Equal(t, cloudwatchlogs.QueryStatusComplete, *result.Status)
	if assert.Len(t, result.Results, 1) {
		if assert.Len(t, result.Results[0], 2) {
			assert.Equal(t, "group", *result.Results[0][0].Field)
			assert.Equal(t, "foobar", *result.Results[0][0].Value)
			assert.Equal(t, "total", *result.Results[0][1].Field)
			assert.Equal(t, "123", *result.Results[0][1].Value)
		}
	}
	assert.Equal(t, 30.0, *result.Statistics.BytesScanned)
	assert.Equal(t, 20.0, *result.Statistics.RecordsScanned)
	assert.Equal(t, 10.0, *result.Statistics.RecordsMatched)
}

func TestRunQuery_returns_error_if_no_success_after_retry(t *testing.T) {
	ctx := context.Background()
	queryID := "my-query-id"

	svc := &mockCWLogsService{
		startQueryWithContext: func(ctx aws.Context, input *cloudwatchlogs.StartQueryInput, opts ...request.Option) (*cloudwatchlogs.StartQueryOutput, error) {
			return &cloudwatchlogs.StartQueryOutput{QueryId: aws.String(queryID)}, nil
		},
		getQueryResultsWithContext: func(ctx aws.Context, input *cloudwatchlogs.GetQueryResultsInput, opts ...request.Option) (*cloudwatchlogs.GetQueryResultsOutput, error) {
			return nil, errors.New("test error")
		},
	}
	logGroupName := "/aws/lambda/my-function"
	query := "filter ... | stats ..."
	timeRange := &QueryTimeRange{
		StartTime: time.Date(2021, time.June, 6, 12, 20, 0, 0, time.UTC),
		EndTime:   time.Date(2021, time.June, 6, 12, 25, 0, 0, time.UTC),
	}

	_, err := RunQuery(ctx, logger, svc, logGroupName, query, timeRange)

	assert.EqualError(t, err, fmt.Sprintf("failed to get query results: test error"))
}

func TestRunQuery_returns_error_if_query_failed(t *testing.T) {
	ctx := context.Background()
	queryID := "my-query-id"
	svc := &mockCWLogsService{
		startQueryWithContext: func(ctx aws.Context, input *cloudwatchlogs.StartQueryInput, opts ...request.Option) (*cloudwatchlogs.StartQueryOutput, error) {
			return &cloudwatchlogs.StartQueryOutput{QueryId: aws.String(queryID)}, nil
		},
		getQueryResultsWithContext: func(ctx aws.Context, input *cloudwatchlogs.GetQueryResultsInput, opts ...request.Option) (*cloudwatchlogs.GetQueryResultsOutput, error) {
			return &cloudwatchlogs.GetQueryResultsOutput{
				Status: aws.String(cloudwatchlogs.QueryStatusFailed),
			}, nil
		},
	}
	logGroupName := "/aws/lambda/my-function"
	query := "filter ... | stats ..."
	timeRange := &QueryTimeRange{
		StartTime: time.Date(2021, time.June, 6, 12, 20, 0, 0, time.UTC),
		EndTime:   time.Date(2021, time.June, 6, 12, 25, 0, 0, time.UTC),
	}

	_, err := RunQuery(ctx, logger, svc, logGroupName, query, timeRange)

	assert.EqualError(t, err, fmt.Sprintf("query failed: %s", cloudwatchlogs.QueryStatusFailed))
}

func TestRunQuery_stops_query_if_canceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan interface{})
	var stopQueryInput *cloudwatchlogs.StopQueryInput
	queryID := "my-query-id"
	svc := &mockCWLogsService{
		startQueryWithContext: func(ctx aws.Context, input *cloudwatchlogs.StartQueryInput, opts ...request.Option) (*cloudwatchlogs.StartQueryOutput, error) {
			return &cloudwatchlogs.StartQueryOutput{QueryId: aws.String(queryID)}, nil
		},
		stopQueryWithContext: func(ctx aws.Context, input *cloudwatchlogs.StopQueryInput, opts ...request.Option) (*cloudwatchlogs.StopQueryOutput, error) {
			stopQueryInput = input
			return &cloudwatchlogs.StopQueryOutput{Success: aws.Bool(true)}, nil
		},
		getQueryResultsWithContext: func(ctx aws.Context, input *cloudwatchlogs.GetQueryResultsInput, opts ...request.Option) (*cloudwatchlogs.GetQueryResultsOutput, error) {
			c <- struct{}{}
			return &cloudwatchlogs.GetQueryResultsOutput{
				Status: aws.String(cloudwatchlogs.QueryStatusScheduled),
			}, nil
		},
	}
	logGroupName := "/aws/lambda/my-function"
	query := "filter ... | stats ..."
	timeRange := &QueryTimeRange{
		StartTime: time.Date(2021, time.June, 6, 12, 20, 0, 0, time.UTC),
		EndTime:   time.Date(2021, time.June, 6, 12, 25, 0, 0, time.UTC),
	}

	go func() {
		<-c
		cancel()
	}()
	_, err := RunQuery(ctx, logger, svc, logGroupName, query, timeRange)

	assert.Equal(t, queryID, *stopQueryInput.QueryId)

	assert.EqualError(t, err, ctx.Err().Error())
}

func TestGenerateMetricData_returns_empty_data_if_result_has_no_rows(t *testing.T) {
	results := [][]*cloudwatchlogs.ResultField{}
	time := time.Date(2021, time.June, 6, 12, 20, 0, 0, time.UTC)

	data, err := GenerateMetricData(logger, results, time, "", "", "")
	assert.NoError(t, err)
	assert.Len(t, data, 0)
}

func TestGenerateMetricData_generates_metric_data_to_post(t *testing.T) {
	results := [][]*cloudwatchlogs.ResultField{
		{
			{
				Field: aws.String("xxx"),
				Value: aws.String("12.3"),
			},
			{
				Field: aws.String("yyy"),
				Value: aws.String("45.6"),
			},
			{
				Field: aws.String("zzz"),
				Value: aws.String("78.9"),
			},
		},
	}
	time := time.Date(2021, time.June, 6, 12, 20, 0, 0, time.UTC)

	data, err := GenerateMetricData(logger, results, time, "", "", "")
	assert.NoError(t, err)
	if assert.Len(t, data, 3) {
		assert.Equal(t, mackerel.MetricValue{
			Name:  "xxx",
			Time:  time.Unix(),
			Value: 12.3,
		}, *data[0])
		assert.Equal(t, mackerel.MetricValue{
			Name:  "yyy",
			Time:  time.Unix(),
			Value: 45.6,
		}, *data[1])
		assert.Equal(t, mackerel.MetricValue{
			Name:  "zzz",
			Time:  time.Unix(),
			Value: 78.9,
		}, *data[2])
	}
}

func TestGenerateMetricData_appends_metric_name_prefix_if_exists(t *testing.T) {
	results := [][]*cloudwatchlogs.ResultField{
		{
			{
				Field: aws.String("xxx"),
				Value: aws.String("12.3"),
			},
			{
				Field: aws.String("yyy"),
				Value: aws.String("45.6"),
			},
			{
				Field: aws.String("zzz"),
				Value: aws.String("78.9"),
			},
		},
	}
	time := time.Date(2021, time.June, 6, 12, 20, 0, 0, time.UTC)

	data, err := GenerateMetricData(logger, results, time, "foobar", "", "")
	assert.NoError(t, err)
	if assert.Len(t, data, 3) {
		assert.Equal(t, mackerel.MetricValue{
			Name:  "foobar.xxx",
			Time:  time.Unix(),
			Value: 12.3,
		}, *data[0])
		assert.Equal(t, mackerel.MetricValue{
			Name:  "foobar.yyy",
			Time:  time.Unix(),
			Value: 45.6,
		}, *data[1])
		assert.Equal(t, mackerel.MetricValue{
			Name:  "foobar.zzz",
			Time:  time.Unix(),
			Value: 78.9,
		}, *data[2])
	}
}

func TestGenerateMetricData_does_not_include_default_field_in_metric_names(t *testing.T) {
	results := [][]*cloudwatchlogs.ResultField{
		{
			{
				Field: aws.String("xxx"),
				Value: aws.String("12.3"),
			},
			{
				Field: aws.String("yyy"),
				Value: aws.String("45.6"),
			},
			{
				Field: aws.String("zzz"),
				Value: aws.String("78.9"),
			},
		},
	}
	time := time.Date(2021, time.June, 6, 12, 20, 0, 0, time.UTC)

	data, err := GenerateMetricData(logger, results, time, "foobar", "", "yyy")
	assert.NoError(t, err)
	if assert.Len(t, data, 3) {
		assert.Equal(t, mackerel.MetricValue{
			Name:  "foobar.xxx",
			Time:  time.Unix(),
			Value: 12.3,
		}, *data[0])
		assert.Equal(t, mackerel.MetricValue{
			Name:  "foobar",
			Time:  time.Unix(),
			Value: 45.6,
		}, *data[1])
		assert.Equal(t, mackerel.MetricValue{
			Name:  "foobar.zzz",
			Time:  time.Unix(),
			Value: 78.9,
		}, *data[2])
	}
}

func TestGenerateMetricData_groups_metrics_by_group_field(t *testing.T) {
	results := [][]*cloudwatchlogs.ResultField{
		{
			{
				Field: aws.String("group"),
				Value: aws.String("xxx"),
			},
			{
				Field: aws.String("processed"),
				Value: aws.String("12.3"),
			},
			{
				Field: aws.String("error"),
				Value: aws.String("0.123"),
			},
		},
		{
			{
				Field: aws.String("group"),
				Value: aws.String("yyy"),
			},
			{
				Field: aws.String("processed"),
				Value: aws.String("45.6"),
			},
			{
				Field: aws.String("error"),
				Value: aws.String("0.456"),
			},
		},
		{
			{
				Field: aws.String("group"),
				Value: aws.String("zzz"),
			},
			{
				Field: aws.String("processed"),
				Value: aws.String("78.9"),
			},
			{
				Field: aws.String("error"),
				Value: aws.String("0.789"),
			},
		},
	}
	time := time.Date(2021, time.June, 6, 12, 20, 0, 0, time.UTC)

	data, err := GenerateMetricData(logger, results, time, "foobar", "group", "processed")
	assert.NoError(t, err)
	if assert.Len(t, data, 6) {
		assert.Equal(t, mackerel.MetricValue{
			Name:  "foobar.xxx",
			Time:  time.Unix(),
			Value: 12.3,
		}, *data[0])
		assert.Equal(t, mackerel.MetricValue{
			Name:  "foobar.xxx.error",
			Time:  time.Unix(),
			Value: 0.123,
		}, *data[1])
		assert.Equal(t, mackerel.MetricValue{
			Name:  "foobar.yyy",
			Time:  time.Unix(),
			Value: 45.6,
		}, *data[2])
		assert.Equal(t, mackerel.MetricValue{
			Name:  "foobar.yyy.error",
			Time:  time.Unix(),
			Value: 0.456,
		}, *data[3])
		assert.Equal(t, mackerel.MetricValue{
			Name:  "foobar.zzz",
			Time:  time.Unix(),
			Value: 78.9,
		}, *data[4])
		assert.Equal(t, mackerel.MetricValue{
			Name:  "foobar.zzz.error",
			Time:  time.Unix(),
			Value: 0.789,
		}, *data[5])
	}
}

func TestGenerateMetricData_skips_fields_that_could_not_be_parsed(t *testing.T) {
	results := [][]*cloudwatchlogs.ResultField{
		{
			{
				Field: aws.String("xxx"),
				Value: aws.String("12.3"),
			},
			{
				Field: aws.String("yyy"),
				Value: aws.String("null"),
			},
			{
				Field: aws.String("zzz"),
				Value: aws.String("78.9"),
			},
		},
	}
	time := time.Date(2021, time.June, 6, 12, 20, 0, 0, time.UTC)

	data, err := GenerateMetricData(logger, results, time, "", "", "")
	assert.NoError(t, err)
	if assert.Len(t, data, 2) {
		assert.Equal(t, mackerel.MetricValue{
			Name:  "xxx",
			Time:  time.Unix(),
			Value: 12.3,
		}, *data[0])
		assert.Equal(t, mackerel.MetricValue{
			Name:  "zzz",
			Time:  time.Unix(),
			Value: 78.9,
		}, *data[1])
	}
}

func TestGenerateMetricData_skips_fields_that_have_empty_metric_name(t *testing.T) {
	results := [][]*cloudwatchlogs.ResultField{
		{
			{
				Field: aws.String("xxx"),
				Value: aws.String("12.3"),
			},
			{
				Field: aws.String("yyy"),
				Value: aws.String("45.6"),
			},
			{
				Field: aws.String("zzz"),
				Value: aws.String("78.9"),
			},
		},
	}
	time := time.Date(2021, time.June, 6, 12, 20, 0, 0, time.UTC)

	data, err := GenerateMetricData(logger, results, time, "", "", "yyy")
	assert.NoError(t, err)
	if assert.Len(t, data, 2) {
		assert.Equal(t, mackerel.MetricValue{
			Name:  "xxx",
			Time:  time.Unix(),
			Value: 12.3,
		}, *data[0])
		assert.Equal(t, mackerel.MetricValue{
			Name:  "zzz",
			Time:  time.Unix(),
			Value: 78.9,
		}, *data[1])
	}
}

func TestGenerateMetricData_skips_fields_that_have_duplicate_metric_name(t *testing.T) {
	results := [][]*cloudwatchlogs.ResultField{
		{
			{
				Field: aws.String("xxx"),
				Value: aws.String("12.3"),
			},
			{
				Field: aws.String("yyy"),
				Value: aws.String("45.6"),
			},
			{
				Field: aws.String("zzz"),
				Value: aws.String("78.9"),
			},
		},
		{
			{
				Field: aws.String("xxx"),
				Value: aws.String("1.23"),
			},
			{
				Field: aws.String("yyy"),
				Value: aws.String("4.56"),
			},
			{
				Field: aws.String("zzz"),
				Value: aws.String("7.89"),
			},
		},
	}
	time := time.Date(2021, time.June, 6, 12, 20, 0, 0, time.UTC)

	data, err := GenerateMetricData(logger, results, time, "", "", "")
	assert.NoError(t, err)
	if assert.Len(t, data, 3) {
		assert.Equal(t, mackerel.MetricValue{
			Name:  "xxx",
			Time:  time.Unix(),
			Value: 12.3,
		}, *data[0])
		assert.Equal(t, mackerel.MetricValue{
			Name:  "yyy",
			Time:  time.Unix(),
			Value: 45.6,
		}, *data[1])
		assert.Equal(t, mackerel.MetricValue{
			Name:  "zzz",
			Time:  time.Unix(),
			Value: 78.9,
		}, *data[2])
	}
}

type mockMackerelClient struct {
	input *postServiceMetricValuesInput
}

type postServiceMetricValuesInput struct {
	serviceName  string
	metricValues []*mackerel.MetricValue
}

func (client *mockMackerelClient) PostServiceMetricValues(serviceName string, metricValues []*mackerel.MetricValue) error {
	client.input = &postServiceMetricValuesInput{serviceName, metricValues}
	return nil
}

func TestPostMetricData_posts_service_metrics(t *testing.T) {
	client := &mockMackerelClient{}
	serviceName := "my-service"
	time := time.Date(2021, time.June, 6, 12, 20, 0, 0, time.UTC)
	data := []*mackerel.MetricValue{
		{
			Name:  "foobar.xxx",
			Time:  time.Unix(),
			Value: 12.3,
		},
	}

	err := PostMetricData(logger, client, serviceName, data)

	assert.Equal(t, serviceName, client.input.serviceName)
	if assert.Len(t, client.input.metricValues, 1) {
		assert.Equal(t, mackerel.MetricValue{
			Name:  "foobar.xxx",
			Time:  time.Unix(),
			Value: 12.3,
		}, *client.input.metricValues[0])
	}

	assert.NoError(t, err)
}

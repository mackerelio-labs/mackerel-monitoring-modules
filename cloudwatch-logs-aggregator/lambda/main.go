package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go/service/ssm"
	"github.com/mackerelio/mackerel-client-go"
	"github.com/sirupsen/logrus"
)

func main() {
	lambda.Start(HandleRequest)
}

type Event struct {
	RuleName string `json:"rule_name"`

	APIBaseURL string `json:"api_base_url"`
	APIKeyName string `json:"api_key_name"`

	LogGroupName      string `json:"log_group_name"`
	Query             string `json:"query"`
	IntervalInMinutes int64  `json:"interval_in_minutes"`
	OffsetInMinutes   int64  `json:"offset_in_minutes"`

	MetricNamePrefix string             `json:"metric_name_prefix"`
	GroupField       string             `json:"group_field"`
	DefaultField     string             `json:"default_field"`
	DefaultMetrics   map[string]float64 `json:"default_metrics"`

	ServiceName string `json:"service_name"`
}

func HandleRequest(ctx context.Context, event Event) error {
	lctx, ok := lambdacontext.FromContext(ctx)
	if !ok {
		return errors.New("failed to get Lambda context")
	}

	logger := createLogger(os.Getenv("LOG_LEVEL"))
	requestLogger := logger.WithFields(logrus.Fields{
		"aws_request_id": lctx.AwsRequestID,
		"rule_name":      event.RuleName,
	})

	err := ValidateInput(&event)
	if err != nil {
		requestLogger.Errorf("validation error: %v", err)
		return err
	}

	sess, err := session.NewSession()
	if err != nil {
		requestLogger.Errorf("failed to create AWS session: %v", err)
		return err
	}

	ssmSvc := ssm.New(sess)
	apiKey, err := GetAPIKey(ctx, ssmSvc, event.APIKeyName)
	if err != nil {
		requestLogger.Errorf("failed to get API key: %v", err)
		return err
	}
	client, err := CreateMackerelClient(event.APIBaseURL, apiKey)
	if err != nil {
		requestLogger.Errorf("failed to create Mackerel client: %v", err)
		return err
	}

	cwLogsSvc := cloudwatchlogs.New(sess)
	timeRange := GetQueryTimeRange(
		time.Now(),
		time.Duration(event.IntervalInMinutes)*time.Minute,
		time.Duration(event.OffsetInMinutes)*time.Minute,
	)
	result, err := RunQuery(ctx, requestLogger, cwLogsSvc, event.LogGroupName, event.Query, timeRange)
	if err != nil {
		requestLogger.Errorf("failed to query: %v", err)
		return err
	}

	data, err := GenerateMetricData(
		requestLogger,
		result.Results, timeRange.StartTime,
		event.MetricNamePrefix, event.GroupField, event.DefaultField, event.DefaultMetrics,
	)
	if err != nil {
		requestLogger.Errorf("failed to generate metric data: %v", err)
		return err
	}
	err = PostMetricData(requestLogger, client, event.ServiceName, data)
	if err != nil {
		requestLogger.Errorf("failed to post metric data: %v", err)
		return err
	}

	return nil
}

func createLogger(levelStr string) *logrus.Logger {
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})
	logger.SetLevel(logrus.InfoLevel)
	if levelStr != "" {
		if level, err := logrus.ParseLevel(levelStr); err == nil {
			logger.SetLevel(level)
		}
	}
	return logger
}

func ValidateInput(event *Event) error {
	if event.APIKeyName == "" {
		return errors.New("api_key_name is required")
	}
	if event.LogGroupName == "" {
		return errors.New("log_group_name is required")
	}
	if event.Query == "" {
		return errors.New("query is required")
	}
	if event.IntervalInMinutes <= 0 {
		return errors.New("interval_in_minutes must be positive")
	}
	if event.OffsetInMinutes < 0 {
		return errors.New("offset_in_minutes must be zero or positive")
	}
	if event.ServiceName == "" {
		return errors.New("service_name is required")
	}
	return nil
}

type SSMService interface {
	GetParameterWithContext(ctx aws.Context, input *ssm.GetParameterInput, opts ...request.Option) (*ssm.GetParameterOutput, error)
}

func GetAPIKey(ctx context.Context, svc SSMService, apiKeyName string) (string, error) {
	p, err := svc.GetParameterWithContext(ctx, &ssm.GetParameterInput{
		Name:           aws.String(apiKeyName),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", err
	}
	return *p.Parameter.Value, nil
}

func CreateMackerelClient(apiBaseURL, apiKey string) (*mackerel.Client, error) {
	client := mackerel.NewClient(apiKey)
	if apiBaseURL != "" {
		u, err := url.Parse(apiBaseURL)
		if err != nil {
			return nil, err
		}
		client.BaseURL = u
	}
	client.UserAgent = "mackerel-cloudwatch-logs-aggregator/" + version
	return client, nil
}

type QueryTimeRange struct {
	// StartTime is inclusive.
	StartTime time.Time
	// EndTime is exclusive.
	EndTime time.Time
}

func GetQueryTimeRange(currentTime time.Time, interval, offset time.Duration) *QueryTimeRange {
	endTime := currentTime.Add(-offset).Truncate(interval)
	startTime := endTime.Add(-interval)
	return &QueryTimeRange{
		StartTime: startTime,
		EndTime:   endTime,
	}
}

type CWLogsService interface {
	StartQueryWithContext(ctx aws.Context, input *cloudwatchlogs.StartQueryInput, opts ...request.Option) (*cloudwatchlogs.StartQueryOutput, error)
	StopQueryWithContext(ctx aws.Context, input *cloudwatchlogs.StopQueryInput, opts ...request.Option) (*cloudwatchlogs.StopQueryOutput, error)
	GetQueryResultsWithContext(ctx aws.Context, input *cloudwatchlogs.GetQueryResultsInput, opts ...request.Option) (*cloudwatchlogs.GetQueryResultsOutput, error)
}

func RunQuery(
	ctx context.Context,
	logger logrus.FieldLogger,
	svc CWLogsService,
	logGroupName, query string,
	timeRange *QueryTimeRange,
) (*cloudwatchlogs.GetQueryResultsOutput, error) {
	q, err := svc.StartQueryWithContext(ctx, &cloudwatchlogs.StartQueryInput{
		LogGroupName: aws.String(logGroupName),
		StartTime:    aws.Int64(timeRange.StartTime.Unix()),
		EndTime:      aws.Int64(timeRange.EndTime.Unix()),
		// Because StartQuery takes a closed interval [StartTime, EndTime],
		// an additional filter is needed to select logs in a half-open interval [StartTime, EndTime).
		QueryString: aws.String(withFilterByTimeRange(query, timeRange)),
	})
	if err != nil {
		// Perhaps we should retry?
		return nil, fmt.Errorf("failed to start query: %w", err)
	}
	queryLogger := logger.WithField("query_id", *q.QueryId)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	retryCount := 0
	for {
		select {
		case <-ctx.Done():
			queryLogger.Debug("stopping query")
			r, err := svc.StopQueryWithContext(context.Background(), &cloudwatchlogs.StopQueryInput{
				QueryId: q.QueryId,
			})
			if err != nil || !*r.Success {
				// Perhaps we should retry?
				queryLogger.Warnf("failed to stop query: %v", err)
			}
			return nil, ctx.Err()
		case <-ticker.C:
			queryLogger.Debug("getting query results")
			r, err := svc.GetQueryResultsWithContext(ctx, &cloudwatchlogs.GetQueryResultsInput{
				QueryId: q.QueryId,
			})
			if err != nil {
				// retry 3 times
				if retryCount < 3 {
					retryCount += 1
					queryLogger.Debugf("failed to get query results; will retry (count = %d)", retryCount)
					continue
				}
				return nil, fmt.Errorf("failed to get query results: %w", err)
			}
			retryCount = 0
			switch *r.Status {
			case cloudwatchlogs.QueryStatusScheduled, cloudwatchlogs.QueryStatusRunning:
				queryLogger.WithField("status", *r.Status).Debug("waiting for query")
				continue
			case cloudwatchlogs.QueryStatusComplete:
				queryLogger.WithFields(logrus.Fields{
					"bytes_scanned":   r.Statistics.BytesScanned,
					"records_scanned": r.Statistics.RecordsScanned,
					"records_matched": r.Statistics.RecordsMatched,
				}).Info("query complete")
				return r, nil
			case cloudwatchlogs.QueryStatusFailed, cloudwatchlogs.QueryStatusCancelled, cloudwatchlogs.QueryStatusTimeout:
				return nil, fmt.Errorf("query failed: %s", *r.Status)
			case cloudwatchlogs.QueryStatusUnknown:
				fallthrough
			default:
				return nil, fmt.Errorf("unexpected query status: %s", *r.Status)
			}
		}
	}
}

func withFilterByTimeRange(query string, timeRange *QueryTimeRange) string {
	var b strings.Builder
	b.WriteString("filter ")
	startMillis := timeRange.StartTime.UnixNano() / int64(time.Millisecond)
	b.WriteString(strconv.FormatInt(startMillis, 10))
	b.WriteString(" <= tomillis(@timestamp) and tomillis(@timestamp) < ")
	endMillis := timeRange.EndTime.UnixNano() / int64(time.Millisecond)
	b.WriteString(strconv.FormatInt(endMillis, 10))
	b.WriteString(" | ")
	b.WriteString(query)
	return b.String()
}

func GenerateMetricData(
	logger logrus.FieldLogger,
	results [][]*cloudwatchlogs.ResultField,
	time time.Time,
	metricNamePrefix, groupField, defaultField string,
	defaultMetrics map[string]float64,
) ([]*mackerel.MetricValue, error) {
	if len(results) == 0 {
		return generateDefaultMetricData(time, defaultMetrics, nil), nil
	}
	data := make([]*mackerel.MetricValue, 0, len(results)*len(results[0]))
	seenMetricNames := make(map[string]struct{})
	for _, row := range results {
		groupName := ""
		for _, column := range row {
			if *column.Field == groupField {
				groupName = *column.Value
				break
			}
		}
		groupLogger := logger
		if groupName != "" {
			groupLogger = groupLogger.WithField("group_name", groupName)
		}
		for _, column := range row {
			if *column.Field == groupField {
				continue
			}
			isDefaultField := *column.Field == defaultField
			name := *column.Field
			valueStr := *column.Value
			value, err := strconv.ParseFloat(valueStr, 64)
			if err != nil {
				groupLogger.WithField("name", name).Warnf("field skipped: failed to parse value %#v", valueStr)
				continue
			}
			metricName := joinMetricName(metricNamePrefix, groupName, name, isDefaultField)
			if metricName == "" {
				groupLogger.WithField("name", name).Warn("field skipped: empty metric name")
				continue
			}
			if _, seen := seenMetricNames[metricName]; seen {
				groupLogger.WithField("name", name).Warnf("field skipped: duplicate metric name %s", metricName)
				continue
			}
			seenMetricNames[metricName] = struct{}{}
			data = append(data, &mackerel.MetricValue{
				Name:  metricName,
				Time:  time.Unix(),
				Value: value,
			})
		}
	}
	defaultData := generateDefaultMetricData(time, defaultMetrics, seenMetricNames)
	return append(data, defaultData...), nil
}

func joinMetricName(metricNamePrefix, groupName, name string, isDefaultField bool) string {
	names := make([]string, 0, 3)
	if metricNamePrefix != "" {
		names = append(names, metricNamePrefix)
	}
	if groupName != "" {
		names = append(names, groupName)
	}
	if !isDefaultField && name != "" {
		names = append(names, name)
	}
	return strings.Join(names, ".")
}

func generateDefaultMetricData(
	time time.Time,
	defaultMetrics map[string]float64,
	seenMetricNames map[string]struct{},
) []*mackerel.MetricValue {
	if len(defaultMetrics) == 0 {
		return nil
	}
	data := make([]*mackerel.MetricValue, 0, len(defaultMetrics))
	for name, value := range defaultMetrics {
		if _, seen := seenMetricNames[name]; !seen {
			data = append(data, &mackerel.MetricValue{
				Name:  name,
				Time:  time.Unix(),
				Value: value,
			})
		}
	}
	return data
}

type MackerelClient interface {
	PostServiceMetricValues(serviceName string, metricValues []*mackerel.MetricValue) error
}

func PostMetricData(
	logger logrus.FieldLogger,
	client MackerelClient,
	serviceName string,
	data []*mackerel.MetricValue,
) error {
	logger.Debug("posting metric data")
	err := client.PostServiceMetricValues(serviceName, data)
	if err == nil {
		logger.WithField("count", len(data)).Infof("posted metric data")
	}
	return err
}

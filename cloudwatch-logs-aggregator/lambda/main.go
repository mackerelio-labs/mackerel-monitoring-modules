package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/mackerelio/mackerel-client-go"
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
	requestLogger := logger.With("aws_request_id", lctx.AwsRequestID).With("rule_name", event.RuleName)

	err := ValidateInput(&event)
	if err != nil {
		requestLogger.Error("validation error", slog.Any("error", err))
		return err
	}

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return err
	}

	ssmSvc := ssm.NewFromConfig(cfg)
	apiKey, err := GetAPIKey(ctx, ssmSvc, event.APIKeyName)
	if err != nil {
		requestLogger.Error("failed to get API key", slog.Any("error", err))
		return err
	}
	client, err := CreateMackerelClient(event.APIBaseURL, apiKey)
	if err != nil {
		requestLogger.Error("failed to create Mackerel client", slog.Any("error", err))
		return err
	}

	cwLogsSvc := cloudwatchlogs.NewFromConfig(cfg)
	timeRange := GetQueryTimeRange(
		time.Now(),
		time.Duration(event.IntervalInMinutes)*time.Minute,
		time.Duration(event.OffsetInMinutes)*time.Minute,
	)
	result, err := RunQuery(ctx, requestLogger, cwLogsSvc, event.LogGroupName, event.Query, timeRange)
	if err != nil {
		requestLogger.Error("failed to query", slog.Any("error", err))
		return err
	}

	data, err := GenerateMetricData(
		requestLogger,
		result.Results, timeRange.StartTime,
		event.MetricNamePrefix, event.GroupField, event.DefaultField, event.DefaultMetrics,
	)
	if err != nil {
		requestLogger.Error("failed to generate metric data", slog.Any("error", err))
		return err
	}
	err = PostMetricData(requestLogger, client, event.ServiceName, data)
	if err != nil {
		requestLogger.Error("failed to post metric data", slog.Any("error", err))
		return err
	}

	return nil
}

func createLogger(levelStr string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if levelStr != "" {
		level := &slog.LevelVar{}
		level.UnmarshalText([]byte(levelStr))
		opts.Level = level
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, opts))
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
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

func GetAPIKey(ctx context.Context, svc SSMService, apiKeyName string) (string, error) {
	p, err := svc.GetParameter(ctx, &ssm.GetParameterInput{
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
	client.UserAgent = "mackerel-cloudwatch-logs-aggregator/" + version()
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
	StartQuery(ctx context.Context, params *cloudwatchlogs.StartQueryInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.StartQueryOutput, error)
	StopQuery(ctx context.Context, params *cloudwatchlogs.StopQueryInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.StopQueryOutput, error)
	GetQueryResults(ctx context.Context, params *cloudwatchlogs.GetQueryResultsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetQueryResultsOutput, error)
}

func RunQuery(
	ctx context.Context,
	logger *slog.Logger,
	svc CWLogsService,
	logGroupName, query string,
	timeRange *QueryTimeRange,
) (*cloudwatchlogs.GetQueryResultsOutput, error) {
	q, err := svc.StartQuery(ctx, &cloudwatchlogs.StartQueryInput{
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
	queryLogger := logger.With("query_id", *q.QueryId)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	retryCount := 0
	for {
		select {
		case <-ctx.Done():
			queryLogger.Debug("stopping query")
			r, err := svc.StopQuery(context.Background(), &cloudwatchlogs.StopQueryInput{
				QueryId: q.QueryId,
			})
			if err != nil || !r.Success {
				// Perhaps we should retry?
				queryLogger.Warn("failed to stop query", slog.Any("error", err))
			}
			return nil, ctx.Err()
		case <-ticker.C:
			queryLogger.Debug("getting query results")
			r, err := svc.GetQueryResults(ctx, &cloudwatchlogs.GetQueryResultsInput{
				QueryId: q.QueryId,
			})
			if err != nil {
				// retry 3 times
				if retryCount < 3 {
					retryCount += 1
					queryLogger.Debug(fmt.Sprintf("failed to get query results; will retry (count = %d)", retryCount))
					continue
				}
				return nil, fmt.Errorf("failed to get query results: %w", err)
			}
			retryCount = 0
			switch r.Status {
			case types.QueryStatusScheduled, types.QueryStatusRunning:
				queryLogger.With("status", r.Status).Debug("waiting for query")
				continue
			case types.QueryStatusComplete:
				queryLogger.With("bytes_scanned", r.Statistics.BytesScanned).
					With("records_scanned", r.Statistics.RecordsScanned).
					With("records_matched", r.Statistics.RecordsMatched).
					Info("query complete")
				return r, nil
			case types.QueryStatusFailed, types.QueryStatusCancelled, types.QueryStatusTimeout:
				return nil, fmt.Errorf("query failed: %s", r.Status)
			case types.QueryStatusUnknown:
				fallthrough
			default:
				return nil, fmt.Errorf("unexpected query status: %s", r.Status)
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
	logger *slog.Logger,
	results [][]types.ResultField,
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
			groupLogger = groupLogger.With("group_name", groupName)
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
				groupLogger.With("name", name).Warn(fmt.Sprintf("field skipped: failed to parse value %#v", valueStr))
				continue
			}
			metricName := sanitizeMetricName(
				joinMetricNameComponents(metricNamePrefix, groupName, name, isDefaultField),
			)
			if metricName == "" {
				groupLogger.With("name", name).Warn("field skipped: empty metric name")
				continue
			}
			if _, seen := seenMetricNames[metricName]; seen {
				groupLogger.With("name", name).Warn(fmt.Sprintf("field skipped: duplicate metric name %s", metricName))
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

func joinMetricNameComponents(metricNamePrefix, groupName, name string, isDefaultField bool) string {
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

var invalidMetricNameCharRegexp = regexp.MustCompile(`[^a-zA-Z0-9\\._\\-]`)

func sanitizeMetricName(name string) string {
	return invalidMetricNameCharRegexp.ReplaceAllString(name, "")
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
	logger *slog.Logger,
	client MackerelClient,
	serviceName string,
	data []*mackerel.MetricValue,
) error {
	logger.Debug("posting metric data")
	err := client.PostServiceMetricValues(serviceName, data)
	if err == nil {
		logger.With("count", len(data)).Info("posted metric data")
	}
	return err
}

func version() (version string) {
	version = "unknown"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	// trim a prefix `v`
	version, _ = strings.CutPrefix(info.Main.Version, "v")

	// strings like "v0.1.2-0.20060102150405-xxxxxxxxxxxx" are long, so they are cut out.
	if strings.Contains(version, "-") {
		index := strings.IndexRune(version, '-')
		version = version[0:index]
	}
	return
}

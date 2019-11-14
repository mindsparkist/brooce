package web

import (
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"brooce/task"

	"github.com/go-redis/redis"
)

type joblistOutputType struct {
	ListType  string
	QueueName string
	Page      int64
	Pages     int64
	Length    int64
	Start     int64
	End       int64
	Query     string

	URL *url.URL

	Jobs []*task.Task
}

func joblistHandler(req *http.Request, rep *httpReply) (err error) {
	path := splitUrlPath(req.URL.Path)
	if len(path) < 2 {
		err = fmt.Errorf("Invalid path")
		return
	}

	listType := path[0]
	queueName := path[1]

	page := joblistQueryParams(req.URL.RawQuery)
	perpage := joblistPerPage(req)

	output := &joblistOutputType{
		QueueName: queueName,
		ListType:  listType,
		Page:      int64(page),
		URL:       req.URL,
	}

	err = output.listJobs(listType == "pending", perpage)
	if err != nil {
		return
	}

	if output.Pages == 0 {
		output.Page = 0
	} else if output.Page > output.Pages {
		output.Page = output.Pages

		err = output.listJobs(listType == "pending", perpage)
		if err != nil {
			return
		}
	}

	err = templates.ExecuteTemplate(rep, "joblist", output)
	return
}

func joblistQueryParams(rq string) (page int) {
	params, err := url.ParseQuery(rq)
	if err != nil {
		log.Printf("Malformed URL query: %s err: %s", rq, err)
		return 1
	}

	page = 1
	if pg, err := strconv.Atoi(params.Get("page")); err == nil && pg > 1 {
		page = pg
	}

	return page
}

func joblistPerPage(req *http.Request) (perpage int64) {
	perpage = 10

	perpageCookie, err := req.Cookie("perpage")
	if err != nil {
		return
	}

	perpage, _ = strconv.ParseInt(perpageCookie.Value, 10, 0)
	if perpage < 1 || perpage > 100 {
		perpage = 10
	}

	return
}

func (output *joblistOutputType) LinkParamsForPage(page int64) template.URL {
	if output.URL == nil {
		return template.URL("")
	}

	q := output.URL.Query()
	q.Set("page", strconv.Itoa(int(page)))

	return template.URL(q.Encode())
}

func (output *joblistOutputType) LinkParamsForPrevPage(page int64) template.URL {
	return output.LinkParamsForPage(page - 1)
}

func (output *joblistOutputType) LinkParamsForNextPage(page int64) template.URL {
	return output.LinkParamsForPage(page + 1)
}

func (output *joblistOutputType) listJobs(reverse bool, perpage int64) (err error) {
	output.Start = (output.Page-1)*perpage + 1
	output.End = output.Page * perpage

	redisKey := fmt.Sprintf("%s:queue:%s:%s", redisHeader, output.QueueName, output.ListType)

	rangeStart := (output.Page - 1) * perpage
	rangeEnd := output.Page*perpage - 1

	if reverse {
		rangeStart, rangeEnd = (rangeEnd+1)*-1, (rangeStart+1)*-1
	}

	var lengthResult *redis.IntCmd
	var rangeResult *redis.StringSliceCmd
	_, err = redisClient.Pipelined(func(pipe redis.Pipeliner) error {
		lengthResult = pipe.LLen(redisKey)
		rangeResult = pipe.LRange(redisKey, rangeStart, rangeEnd)
		return nil
	})
	if err != nil {
		return
	}

	output.Length = lengthResult.Val()
	output.Pages = int64(math.Ceil(float64(output.Length) / float64(perpage)))
	if output.End > output.Length {
		output.End = output.Length
	}
	if output.Start > output.Length {
		output.Start = output.Length
	}

	rangeLength := len(rangeResult.Val())
	output.Jobs = make([]*task.Task, rangeLength)

	if len(output.Jobs) == 0 {
		return
	}

	for i, value := range rangeResult.Val() {
		job, err := task.NewFromJson(value, output.QueueName)
		if err != nil {
			continue
		}

		if reverse {
			output.Jobs[rangeLength-i-1] = job
		} else {
			output.Jobs[i] = job
		}
	}

	task.PopulateHasLog(output.Jobs)
	return
}

```shell
ollama run --verbose starcoder2:7b “Implement a small Java service component that validates incoming user input, applies a simple business rule, and returns a structured result object. Use clean class design, include basic error handling, and keep the code suitable for a typical Spring-style backend, but do not use any external libraries.”
```

I think the description of this task is too generic and vague to give me any good guidelines about how to implement the given task. Also, I’m not
sure what to expect from this task - just to create a component which should be able to validate some input and apply simple business rule? Or it
should provide some service (e.g. RESTful API) and also include basic error handling?

I can think of some useful approaches:
- to mock the application and do unit testing
- to write acceptance tests
- to use tools like JMeter or JMeter-Plugin for Eclipse
- to check the input on server side
- to validate the input by client side with the help of JavaScript.

I assume that this task is a part of an exam, so I suppose it’s also necessary to follow some requirements, which are listed in
<http://www.jukes.com/examnotes/java_spring_interview/> and <https://www.javacodegeeks.com/2014/08/java-web-application-frameworks-comparison.html>.

---
**Token Throughput**:
- total duration:       13.890226835s
- load duration:        9.284980119s
- prompt eval count:    66 token(s)
- prompt eval duration: 35.585091ms
- prompt eval rate:     1854.71 tokens/s
- eval count:           236 token(s)
- eval duration:        4.500437326s
- eval rate:            52.44 tokens/s
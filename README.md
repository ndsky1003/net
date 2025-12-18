# net
职责分工的一个tcp，不涉及任何业务

#### 主要区别
    1. 将读取的内容buf分配交给上游,做到真正的zero_copy

#### install
> go install github.com/ndsky1003/net/v2

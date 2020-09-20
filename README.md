# sqlds

`sqlds` stands for `SQL Datasource`.

Most SQL-driven datasources, like `Oracle`, `Snowflake`, `DB2`, and etc, share extremely similar codebases.

The `sqlds` package is intended to remove the repitition of these datasources and centralize the datasource logic. The only thing that the datasources themselves should have to define is how to connect to the database and what driver to use.
